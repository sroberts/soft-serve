package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"hash"
	"strings"
	"time"

	"github.com/charmbracelet/soft-serve/git"
	gossh "golang.org/x/crypto/ssh"
)

// Commit signatures. An SSH signature is checked here, against the SSH keys
// of this server's users: "verified" means the signature is good and made
// with a key some user here has, and names them. It says nothing of the
// commit's author or committer fields, which anyone can write. GPG and
// X.509 signatures are reported without being checked: there is no keyring
// here to check them against.

// CommitSignature is how a commit was signed.
type CommitSignature struct {
	// Format is ssh, gpg or x509.
	Format string `json:"format"`
	// Status is verified (a good signature by a key a user here has),
	// unverified (good, but no user here has the key, or not checked at
	// all) or bad (it doesn't match the commit).
	Status string `json:"status"`
	// Signer is the Soft Serve user whose key made a verified signature.
	Signer string `json:"signer,omitempty"`
	// Key is the SHA256 fingerprint of an SSH signature's key.
	Key    string `json:"key,omitempty"`
	Reason string `json:"reason,omitempty"`
}

const (
	sigVerified   = "verified"
	sigUnverified = "unverified"
	sigBad        = "bad"
)

// commitSignature reads and checks the signature on the commit sha, or
// returns nil when it isn't signed or can't be read.
func (b *browser) commitSignature(ctx context.Context, gr *git.Repository, sha string) *CommitSignature {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := gitOutput(ctx, gr.Path, "cat-file", "commit", sha)
	if err != nil {
		return nil
	}
	sig, payload := splitSignature(raw)
	if sig == "" {
		return nil
	}
	switch {
	case strings.HasPrefix(sig, "-----BEGIN SSH SIGNATURE-----"):
		return b.checkSSHSignature(ctx, sig, payload)
	case strings.HasPrefix(sig, "-----BEGIN PGP SIGNATURE-----"):
		return &CommitSignature{Format: "gpg", Status: sigUnverified, Reason: "GPG signatures aren't checked on this server"}
	case strings.HasPrefix(sig, "-----BEGIN SIGNED MESSAGE-----"):
		return &CommitSignature{Format: "x509", Status: sigUnverified, Reason: "X.509 signatures aren't checked on this server"}
	}
	return &CommitSignature{Format: "unknown", Status: sigUnverified, Reason: "the signature's format isn't one this server knows"}
}

// splitSignature takes a commit object apart into its signature and the
// bytes that were signed: the object without the signature header.
func splitSignature(raw []byte) (string, []byte) {
	head, body, ok := bytes.Cut(raw, []byte("\n\n"))
	if !ok {
		head, body = raw, nil
	}
	lines := bytes.SplitAfter(head, []byte("\n"))
	var sig strings.Builder
	var payload bytes.Buffer
	inSig := false
	for _, l := range lines {
		switch {
		case bytes.HasPrefix(l, []byte("gpgsig ")) && sig.Len() == 0:
			inSig = true
			sig.Write(bytes.TrimPrefix(l, []byte("gpgsig ")))
		case inSig && bytes.HasPrefix(l, []byte(" ")):
			sig.Write(l[1:])
		default:
			inSig = false
			payload.Write(l)
		}
	}
	if sig.Len() == 0 {
		return "", nil
	}
	if ok {
		if !bytes.HasSuffix(payload.Bytes(), []byte("\n")) {
			payload.WriteByte('\n')
		}
		payload.WriteByte('\n')
		payload.Write(body)
	}
	return sig.String(), payload.Bytes()
}

func (b *browser) checkSSHSignature(ctx context.Context, armored string, payload []byte) *CommitSignature {
	out := &CommitSignature{Format: "ssh"}
	pk, err := verifySSHSignature(armored, "git", payload)
	if pk != nil {
		out.Key = gossh.FingerprintSHA256(pk)
	}
	if err != nil {
		out.Status = sigBad
		out.Reason = err.Error()
		return out
	}
	u, err := b.be.UserByPublicKey(ctx, pk)
	if err != nil || u == nil {
		out.Status = sigUnverified
		out.Reason = "no user on this server has the key that made it"
		return out
	}
	out.Status = sigVerified
	out.Signer = u.Username()
	return out
}

// sshSigMagic starts every SSH signature (PROTOCOL.sshsig).
const sshSigMagic = "SSHSIG"

var errBadSignature = errors.New("the signature doesn't match the commit")

// verifySSHSignature checks an armored SSH signature over message in
// namespace, and returns the key that made it. A signature that can't be
// read returns no key.
func verifySSHSignature(armored, namespace string, message []byte) (gossh.PublicKey, error) {
	body := strings.TrimSpace(armored)
	body, ok := strings.CutPrefix(body, "-----BEGIN SSH SIGNATURE-----")
	if !ok {
		return nil, errors.New("the signature isn't an SSH signature")
	}
	body, ok = strings.CutSuffix(body, "-----END SSH SIGNATURE-----")
	if !ok {
		return nil, errors.New("the signature is cut short")
	}
	blob, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(body), ""))
	if err != nil || !bytes.HasPrefix(blob, []byte(sshSigMagic)) {
		return nil, errors.New("the signature can't be read")
	}
	var s struct {
		Version   uint32
		PublicKey []byte
		Namespace string
		Reserved  string
		HashAlg   string
		Signature []byte
	}
	if err := gossh.Unmarshal(blob[len(sshSigMagic):], &s); err != nil || s.Version != 1 {
		return nil, errors.New("the signature can't be read")
	}
	pk, err := gossh.ParsePublicKey(s.PublicKey)
	if err != nil {
		return nil, errors.New("the signature's key can't be read")
	}
	if s.Namespace != namespace {
		return pk, errors.New("the signature wasn't made for git")
	}
	var h hash.Hash
	switch s.HashAlg {
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	default:
		return pk, errors.New("the signature uses an unknown hash")
	}
	h.Write(message)
	signed := append([]byte(sshSigMagic), gossh.Marshal(struct {
		Namespace string
		Reserved  string
		HashAlg   string
		Hash      []byte
	}{s.Namespace, s.Reserved, s.HashAlg, h.Sum(nil)})...)
	var sig gossh.Signature
	if err := gossh.Unmarshal(s.Signature, &sig); err != nil {
		return pk, errors.New("the signature can't be read")
	}
	if err := pk.Verify(signed, &sig); err != nil {
		return pk, errBadSignature
	}
	return pk, nil
}
