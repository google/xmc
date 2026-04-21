package validator

import (
	"fmt"

	"task/go/status"
)

const (
	// defaultMaxPageSize is the maximum page size for list requests.
	defaultMaxPageSize = 1000
	// maxPageTokenLen is the maximum allowed length of a page token string.
	maxPageTokenLen = 256
)

// ValidateAIPCompliantList checks if a list request's parameters are IAP compliant.
// See https://google.aip.dev/158 for more details on pagination and https://google.aip.dev/160 for more details on filtering and ordering.
func ValidateAIPCompliantList(pageSize int32, filter string, orderBy string, pageToken string) error {
	return validateAIPCompliantList(pageSize, filter, orderBy, pageToken, defaultMaxPageSize)
}

// ValidateAIPCompliantListWithMaxPageSize checks if a list request's parameters are IAP compliant with a custom max page size.
// See https://google.aip.dev/158 for more details on pagination and https://google.aip.dev/160 for more details on filtering and ordering.
func ValidateAIPCompliantListWithMaxPageSize(pageSize int32, filter string, orderBy string, pageToken string, maxPageSize int32) error {
	return validateAIPCompliantList(pageSize, filter, orderBy, pageToken, maxPageSize)
}

func validateAIPCompliantList(pageSize int32, filter string, orderBy string, pageToken string, maxPageSize int32) error {
	if pageSize < 0 {
		return fmt.Errorf("page_size cannot be negative %d: %w", pageSize, status.ErrInvalidArgument)
	}
	if pageSize > maxPageSize {
		return fmt.Errorf("page_size cannot be greater than %d: %w", maxPageSize, status.ErrInvalidArgument)
	}
	if len(pageToken) > maxPageTokenLen {
		return fmt.Errorf("page_token is too long: got %d chars, max is %d: %w", len(pageToken), maxPageTokenLen, status.ErrInvalidArgument)
	}
	return nil
}
