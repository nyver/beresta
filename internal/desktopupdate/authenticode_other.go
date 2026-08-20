//go:build !windows

package desktopupdate

import (
	"context"
	"errors"
)

type AuthenticodeVerifier struct{}

func (AuthenticodeVerifier) VerifyPublisher(context.Context, string) error {
	return errors.New("Authenticode verification is available only on Windows")
}
