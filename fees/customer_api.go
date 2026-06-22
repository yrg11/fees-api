package fees

import (
	"context"
	"errors"

	"encore.dev/beta/errs"
)

type CreateCustomerRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type CreateCustomerResponse struct {
	Customer Customer `json:"customer"`
	APIKey   string   `json:"api_key"` // Only returned once at creation
}

// CreateCustomer registers a new customer and returns their API key.
// The API key is only shown once — store it securely.
//
//encore:api public method=POST path=/customers
func CreateCustomer(ctx context.Context, req *CreateCustomerRequest) (*CreateCustomerResponse, error) {
	if req.Name == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "name is required"}
	}
	if req.Email == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "email is required"}
	}

	customer, apiKey, err := createCustomer(ctx, req.Name, req.Email)
	if err != nil {
		if errors.Is(err, ErrEmailAlreadyTaken) {
			return nil, &errs.Error{Code: errs.AlreadyExists, Message: err.Error()}
		}
		return nil, &errs.Error{Code: errs.Internal, Message: err.Error()}
	}

	logAuditEvent(ctx, customer.ID, AuditEventCustomerCreated, AuditDetail{
		"email": req.Email,
	})

	return &CreateCustomerResponse{
		Customer: customer,
		APIKey:   apiKey,
	}, nil
}

type RotateKeyResponse struct {
	APIKey              string `json:"api_key"`
	PreviousKeyRevoked bool   `json:"previous_key_revoked"`
}

// RotateKey generates a new API key for the authenticated customer.
// The old key is immediately revoked.
//
//encore:api auth method=POST path=/customers/rotate-key
func RotateKey(ctx context.Context) (*RotateKeyResponse, error) {
	customerID := getAuthCustomerID()

	apiKey, err := rotateAPIKey(ctx, customerID)
	if err != nil {
		if errors.Is(err, ErrCustomerNotFound) {
			return nil, &errs.Error{Code: errs.NotFound, Message: err.Error()}
		}
		return nil, &errs.Error{Code: errs.Internal, Message: err.Error()}
	}

	logAuditEvent(ctx, customerID, AuditEventKeyRotated, AuditDetail{
		"new_key_prefix": apiKeyPrefix(apiKey),
	})

	return &RotateKeyResponse{
		APIKey:              apiKey,
		PreviousKeyRevoked: true,
	}, nil
}

type GetCustomerResponse struct {
	Customer Customer `json:"customer"`
}

// GetCustomer returns the authenticated customer's details.
//
//encore:api auth method=GET path=/customers/me
func GetCustomer(ctx context.Context) (*GetCustomerResponse, error) {
	customerID := getAuthCustomerID()

	customer, err := getCustomerByID(ctx, customerID)
	if err != nil {
		return nil, &errs.Error{Code: errs.Internal, Message: err.Error()}
	}

	return &GetCustomerResponse{Customer: customer}, nil
}
