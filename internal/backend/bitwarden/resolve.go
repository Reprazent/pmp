package bitwarden

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/ref"
	"github.com/Reprazent/pmp/internal/totp"
)

type item struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	FolderID       *string  `json:"folderId"`
	OrganizationID *string  `json:"organizationId"`
	CollectionIDs  []string `json:"collectionIds"`
	Notes          *string  `json:"notes"`
	Login          *struct {
		Username *string `json:"username"`
		Password *string `json:"password"`
		TOTP     *string `json:"totp"`
		URIs     []struct {
			URI *string `json:"uri"`
		} `json:"uris"`
	} `json:"login"`
	SSHKey *struct {
		PrivateKey  *string `json:"privateKey"`
		PublicKey   *string `json:"publicKey"`
		Fingerprint *string `json:"keyFingerprint"`
	} `json:"sshKey"`
	Fields []struct {
		Name     string  `json:"name"`
		Value    *string `json:"value"`
		Type     int     `json:"type"`
		LinkedID *int    `json:"linkedId"`
	} `json:"fields"`
}

type named struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	OrganizationID *string `json:"organizationId"`
}

// vault is what one Resolve call loads from bw.
type vault struct {
	items       []item
	folders     []named
	collections []named
	orgs        []named
}

// Resolve loads the vault with as few bw calls as possible (items, plus
// folders/collections/organizations only when a reference needs them) and
// picks the fields out locally.
func (b *Backend) Resolve(ctx context.Context, refs []ref.Ref) (map[string]string, error) {
	if err := b.Prepare(ctx, refs); err != nil {
		return nil, err
	}
	settings := backend.SettingsFrom(ctx)

	var errs backend.MultiError
	queries := make([]query, 0, len(refs))
	var needFolders, needCollections, needOrgs bool
	for _, r := range refs {
		q, err := parseRef(r)
		if err != nil {
			errs = append(errs, &backend.RefError{Ref: r.Raw, Err: err})
			continue
		}
		needFolders = needFolders || q.hasFolder
		needCollections = needCollections || q.collection != ""
		needOrgs = needOrgs || q.org != ""
		queries = append(queries, q)
	}
	if len(errs) > 0 {
		return nil, errs
	}

	v, err := b.load(ctx, needFolders, needCollections, needOrgs)
	if err != nil {
		return nil, err
	}

	out := map[string]string{}
	for _, q := range queries {
		it, err := v.find(q)
		if err == nil && q.id != "" && it.Name != q.item {
			settings.Warnf("%s: item %s is called %q now", q.raw, q.id, it.Name)
		}
		var val string
		if err == nil {
			val, err = b.field(ctx, it, q.field)
		}
		if err != nil {
			errs = append(errs, &backend.RefError{Ref: q.raw, Err: err})
			continue
		}
		out[q.raw] = val
	}
	return out, errs.ErrOrNil()
}

func (b *Backend) load(ctx context.Context, folders, collections, orgs bool) (*vault, error) {
	v := &vault{}
	type job struct {
		args []string
		dst  any
	}
	jobs := []job{{[]string{"list", "items"}, &v.items}}
	if folders {
		jobs = append(jobs, job{[]string{"list", "folders"}, &v.folders})
	}
	if collections {
		jobs = append(jobs, job{[]string{"list", "collections"}, &v.collections})
	}
	if orgs {
		jobs = append(jobs, job{[]string{"list", "organizations"}, &v.orgs})
	}

	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := b.run(ctx, b.session, j.args...)
			if err != nil {
				errs[i] = err
				return
			}
			if err := json.Unmarshal(res.Stdout, j.dst); err != nil {
				errs[i] = fmt.Errorf("%s: parsing `bw %s`: %w", scheme, strings.Join(j.args, " "), err)
			}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *vault) find(q query) (item, error) {
	if q.id != "" {
		for _, it := range v.items {
			if it.ID == q.id {
				return it, nil
			}
		}
		return item{}, fmt.Errorf("%w: no item with id %s", backend.ErrNotFound, q.id)
	}

	var orgIDs map[string]bool
	if q.org != "" {
		orgIDs = idsNamed(v.orgs, q.org, nil)
		if len(orgIDs) == 0 {
			return item{}, fmt.Errorf("%w: no organization %q", backend.ErrNotFound, q.org)
		}
	}
	var folderIDs, collectionIDs map[string]bool
	if q.hasFolder {
		folderIDs = idsNamed(v.folders, q.folder, nil)
		if len(folderIDs) == 0 {
			return item{}, fmt.Errorf("%w: no folder %q", backend.ErrNotFound, q.folder)
		}
	}
	if q.collection != "" {
		collectionIDs = idsNamed(v.collections, q.collection, orgIDs)
		if len(collectionIDs) == 0 {
			return item{}, fmt.Errorf("%w: no collection %q", backend.ErrNotFound, q.collection)
		}
	}

	var matches []item
	for _, it := range v.items {
		if it.Name != q.item {
			continue
		}
		if folderIDs != nil && (it.FolderID == nil || !folderIDs[*it.FolderID]) {
			continue
		}
		if collectionIDs != nil && !anyIn(it.CollectionIDs, collectionIDs) {
			continue
		}
		if orgIDs != nil && (it.OrganizationID == nil || !orgIDs[*it.OrganizationID]) {
			continue
		}
		matches = append(matches, it)
	}
	switch len(matches) {
	case 0:
		return item{}, fmt.Errorf("%w: no item %q", backend.ErrNotFound, q.item)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ID
		}
		return item{}, fmt.Errorf("%w; add ?id=<id> (or folder=/collection=) to pick one",
			&backend.AmbiguousError{Candidates: ids})
	}
}

func idsNamed(list []named, name string, orgIDs map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, n := range list {
		if n.Name != name {
			continue
		}
		if orgIDs != nil && (n.OrganizationID == nil || !orgIDs[*n.OrganizationID]) {
			continue
		}
		out[n.ID] = true
	}
	return out
}

func anyIn(ids []string, set map[string]bool) bool {
	for _, id := range ids {
		if set[id] {
			return true
		}
	}
	return false
}

// Bitwarden's linked field ids for login items.
const (
	linkedUsername = 100
	linkedPassword = 101
)

func (b *Backend) field(ctx context.Context, it item, name string) (string, error) {
	notFound := func(what string) (string, error) {
		return "", fmt.Errorf("%w: item %q has no %s", backend.ErrNotFound, it.Name, what)
	}
	login := it.Login
	switch name {
	case fieldUsername:
		if login == nil || login.Username == nil {
			return notFound("username")
		}
		return *login.Username, nil
	case fieldPassword:
		if login == nil || login.Password == nil {
			return notFound("password")
		}
		return *login.Password, nil
	case fieldNotes:
		if it.Notes == nil {
			return notFound("notes")
		}
		return *it.Notes, nil
	case fieldPrivateKey, fieldPublicKey, fieldFingerprint:
		var v *string
		if it.SSHKey != nil {
			switch name {
			case fieldPrivateKey:
				v = it.SSHKey.PrivateKey
			case fieldPublicKey:
				v = it.SSHKey.PublicKey
			default:
				v = it.SSHKey.Fingerprint
			}
		}
		if v == nil {
			return notFound(name)
		}
		return *v, nil
	case fieldURI:
		if login == nil {
			return notFound("uri")
		}
		for _, u := range login.URIs {
			if u.URI != nil {
				return *u.URI, nil
			}
		}
		return notFound("uri")
	case fieldTOTP:
		if login == nil || login.TOTP == nil || *login.TOTP == "" {
			return notFound("totp")
		}
		code, err := totp.Generate(*login.TOTP, time.Now())
		if errors.Is(err, totp.ErrUnsupported) {
			// Steam and friends: let bw do it.
			res, err := b.run(ctx, b.session, "get", "totp", it.ID)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(res.Stdout)), nil
		}
		return code, err
	}

	var found []string
	for _, f := range it.Fields {
		if f.Name != name {
			continue
		}
		switch {
		case f.LinkedID != nil && *f.LinkedID == linkedUsername:
			return b.field(ctx, it, fieldUsername)
		case f.LinkedID != nil && *f.LinkedID == linkedPassword:
			return b.field(ctx, it, fieldPassword)
		case f.Value == nil:
			found = append(found, "")
		default:
			found = append(found, *f.Value)
		}
	}
	switch len(found) {
	case 0:
		return notFound(fmt.Sprintf("field %q", name))
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%w: item %q has %d fields called %q", backend.ErrAmbiguous, it.Name, len(found), name)
	}
}
