package bitwarden

import (
	"fmt"
	"net/url"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/ref"
)

// Reserved field names. Anything else is a custom field.
const (
	fieldUsername = "username"
	fieldPassword = "password"
	fieldTOTP     = "totp"
	fieldNotes    = "notes"
	fieldURI      = "uri"

	// SSH key items.
	fieldPrivateKey  = "private_key"
	fieldPublicKey   = "public_key"
	fieldFingerprint = "fingerprint"
)

// query is a parsed bw:// reference:
//
//	bw://<item>/<field>[?folder=<name>|collection=<name>][&org=<name>]
//	bw://<item>/<field>?id=<uuid>
type query struct {
	raw        string
	item       string
	field      string
	id         string
	folder     string
	collection string
	org        string
	hasFolder  bool
}

func parseRef(r ref.Ref) (query, error) {
	invalid := func(format string, args ...any) (query, error) {
		return query{}, fmt.Errorf("%w: %s", backend.ErrInvalidRef, fmt.Sprintf(format, args...))
	}
	segs, err := r.Segments()
	if err != nil {
		return query{}, err
	}
	if len(segs) != 2 || segs[0] == "" || segs[1] == "" {
		return invalid("want bw://<item>/<field>; encode a / in a name as %%2F")
	}
	q := query{raw: r.Raw, item: segs[0], field: segs[1]}

	values, err := url.ParseQuery(r.RawQuery)
	if err != nil {
		return invalid("bad query: %v", err)
	}
	for k, v := range values {
		if len(v) != 1 {
			return invalid("%s given more than once", k)
		}
		switch k {
		case "id":
			q.id = v[0]
		case "folder":
			q.folder, q.hasFolder = v[0], true
		case "collection":
			q.collection = v[0]
		case "org":
			q.org = v[0]
		default:
			return invalid("unknown parameter %q (want id, folder, collection or org)", k)
		}
	}
	if q.hasFolder && q.collection != "" {
		return invalid("use folder or collection, not both")
	}
	if q.id != "" && (q.hasFolder || q.collection != "" || q.org != "") {
		return invalid("id can't be combined with folder, collection or org")
	}
	return q, nil
}
