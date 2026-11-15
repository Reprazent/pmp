package onepassword

import (
	"context"
	"errors"
	"fmt"

	op "github.com/1password/onepassword-sdk-go"

	"github.com/Reprazent/pmp/internal/backend"
)

// SDKAuth says how to authenticate the SDK client. Exactly one is set.
type SDKAuth struct {
	ServiceAccountToken string
	Account             string
}

// SecretsResolver is the part of the SDK pmp uses.
type SecretsResolver interface {
	ResolveAll(ctx context.Context, refs []string) (op.ResolveAllResponse, error)
}

func newRealSDK(ctx context.Context, auth SDKAuth, version string) (SecretsResolver, error) {
	opts := []op.ClientOption{op.WithIntegrationInfo("pmp", version)}
	if auth.ServiceAccountToken != "" {
		opts = append(opts, op.WithServiceAccountToken(auth.ServiceAccountToken))
	} else {
		// Without cgo this option returns an error, which makes pmp fall
		// back to the op CLI.
		opts = append(opts, op.WithDesktopAppIntegration(auth.Account))
	}
	client, err := op.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return client.Secrets(), nil
}

type sdkStrategy struct {
	client SecretsResolver
	label  string
}

func (s *sdkStrategy) name() string { return s.label }

func (s *sdkStrategy) status(ctx context.Context) backend.Status {
	// Creating the client already authenticated; there's no cheap "whoami"
	// in the SDK, so that's the check.
	_ = ctx
	return backend.Status{Available: true, Detail: "ready"}
}

func (s *sdkStrategy) resolve(ctx context.Context, refs []string) (map[string]string, error) {
	resp, err := s.client.ResolveAll(ctx, refs)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s: %w", scheme, backend.ErrTimeout)
		}
		return nil, fmt.Errorf("%s: %w", scheme, err)
	}
	out := map[string]string{}
	var errs backend.MultiError
	for _, r := range refs {
		res, ok := resp.IndividualResponses[r]
		switch {
		case !ok:
			errs = append(errs, &backend.RefError{Ref: r, Err: fmt.Errorf("%w: no response from 1Password", backend.ErrNotFound)})
		case res.Error != nil:
			errs = append(errs, &backend.RefError{Ref: r, Err: sdkError(*res.Error)})
		case res.Content == nil:
			errs = append(errs, &backend.RefError{Ref: r, Err: fmt.Errorf("%w: empty response from 1Password", backend.ErrNotFound)})
		default:
			out[r] = res.Content.Secret
		}
	}
	return out, errs.ErrOrNil()
}

func sdkError(e op.ResolveReferenceError) error {
	switch e.Type {
	case op.ResolveReferenceErrorTypeVariantParsing:
		return fmt.Errorf("%w: %s", backend.ErrInvalidRef, parsingMessage(e))
	case op.ResolveReferenceErrorTypeVariantVaultNotFound:
		return fmt.Errorf("%w: no such vault", backend.ErrNotFound)
	case op.ResolveReferenceErrorTypeVariantItemNotFound:
		return fmt.Errorf("%w: no such item", backend.ErrNotFound)
	case op.ResolveReferenceErrorTypeVariantFieldNotFound:
		return fmt.Errorf("%w: no such field", backend.ErrNotFound)
	case op.ResolveReferenceErrorTypeVariantNoMatchingSections:
		return fmt.Errorf("%w: no such section", backend.ErrNotFound)
	case op.ResolveReferenceErrorTypeVariantTooManyVaults:
		return fmt.Errorf("%w: more than one vault matches", backend.ErrAmbiguous)
	case op.ResolveReferenceErrorTypeVariantTooManyItems:
		return fmt.Errorf("%w: more than one item matches", backend.ErrAmbiguous)
	case op.ResolveReferenceErrorTypeVariantTooManyMatchingFields:
		return fmt.Errorf("%w: more than one field matches", backend.ErrAmbiguous)
	default:
		return fmt.Errorf("1Password: %s", e.Type)
	}
}

// parsingMessage returns the parse error's message. The SDK's accessor
// panics when the message is missing, hence the recover.
func parsingMessage(e op.ResolveReferenceError) (msg string) {
	defer func() {
		if recover() != nil {
			msg = "could not parse reference"
		}
	}()
	return string(e.Parsing())
}
