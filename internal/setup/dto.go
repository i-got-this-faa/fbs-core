package setup

import "github.com/i-got-this-faa/fbs/internal/metadata"

type statusResponse struct {
	BootstrapRequired bool   `json:"bootstrap_required"`
	Region            string `json:"region"`
	ManagementURL     string `json:"management_url"`
	S3URL             string `json:"s3_url"`
}

type bootstrapRequest struct {
	DisplayName string `json:"display_name"`
}

type bootstrapResponse struct {
	Key           keyResponse   `json:"key"`
	BearerToken   string        `json:"bearer_token"`
	SigV4         sigv4Response `json:"sigv4"`
	Region        string        `json:"region"`
	ManagementURL string        `json:"management_url"`
	S3URL         string        `json:"s3_url"`
}

type keyResponse struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name"`
	AccessKeyID      string `json:"access_key_id"`
	SigV4AccessKeyID string `json:"sigv4_access_key_id"`
	Role             string `json:"role"`
	IsActive         bool   `json:"is_active"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type sigv4Response struct {
	AccessKeyID string `json:"access_key_id"`
	SecretKey   string `json:"secret_key"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func keyDTO(user metadata.User) keyResponse {
	return keyResponse{
		ID:               user.ID,
		DisplayName:      user.DisplayName,
		AccessKeyID:      user.AccessKeyID,
		SigV4AccessKeyID: user.SigV4AccessKeyID,
		Role:             user.Role,
		IsActive:         user.IsActive,
		CreatedAt:        formatTime(user.CreatedAt),
		UpdatedAt:        formatTime(user.UpdatedAt),
	}
}
