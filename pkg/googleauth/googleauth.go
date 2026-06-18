package googleauth

import "context"

type Payload struct {
	Subject string
	Claims  map[string]interface{}
}

func VerifyIDToken(ctx context.Context, idToken, clientID string) (*Payload, error) {
	return &Payload{Claims: make(map[string]interface{})}, nil
}
