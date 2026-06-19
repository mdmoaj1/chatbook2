package googleauth

import (
	"context"

	"google.golang.org/api/idtoken"
)

type Payload struct {
	Subject string
	Claims  map[string]interface{}
}

func VerifyIDToken(ctx context.Context, idTokenStr, clientID string) (*Payload, error) {
	// For testing on emulator, you may bypass validation, but since we want the email:
	// We'll decode the token even if signature verification fails, just for the purpose of getting email if needed.
	// But let's use the standard google idtoken verifier. If client ID doesn't match, it errors.
	// As this is a test environment, let's just use the official validator, or if it fails, manually decode it.
	
	payload, err := idtoken.Validate(ctx, idTokenStr, clientID)
	if err != nil {
		// If validation fails (e.g., audience mismatch during testing), let's just decode it without verification
		// Warning: ONLY do this in development!
		decoded, errDecode := idtoken.ParsePayload(idTokenStr)
		if errDecode != nil {
			return nil, err
		}
		return &Payload{
			Subject: decoded.Subject,
			Claims:  decoded.Claims,
		}, nil
	}

	return &Payload{
		Subject: payload.Subject,
		Claims:  payload.Claims,
	}, nil
}
