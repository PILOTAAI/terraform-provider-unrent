package client

import (
	"bytes"
	"fmt"
	"io"
	"regexp"

	"filippo.io/age"
	"filippo.io/age/armor"
)

var recipientRe = regexp.MustCompile(`^age1[a-z0-9]{58}$`)

// Seal encrypts a secret value to a server's public age recipient (ASCII-armored), as `unrent env set --secret`
// and the console do: only that server can open it; the control plane stores and forwards ciphertext.
func Seal(recipient, value string) (string, error) {
	if !recipientRe.MatchString(recipient) {
		return "", fmt.Errorf("the server's public key %q is not an age X25519 recipient", recipient)
	}
	r, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return "", fmt.Errorf("server key: %w", err)
	}
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, r)
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(w, value); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	if err := aw.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}
