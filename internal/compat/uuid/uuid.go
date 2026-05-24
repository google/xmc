package uuid

import "github.com/google/uuid"

type UUID = string

func New() UUID {
	return uuid.New().String()
}

func NewString() string {
	return uuid.NewString()
}

func Parse(s string) (UUID, error) {
	_, err := uuid.Parse(s)
	if err != nil {
		return "", err
	}
	return s, nil
}
