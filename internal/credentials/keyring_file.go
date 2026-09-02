package credentials

import (
	"fmt"
	"io"
	"os"
)

// insecureFileModeMask flags group- or world-writable permission bits. It is
// deliberately narrow: it does not require any particular read mode (0400,
// 0440, 0444, and similar are all accepted), because Docker and Kubernetes
// secret mounts commonly present files with modes this package does not
// control and has no reason to reject. What has no legitimate reason to be
// set on a keyring file is "anyone other than its owner can write to it".
const insecureFileModeMask = 0o022

// LoadKeyringFile reads and validates the keyring document at path.
//
// Symlinks are followed, not rejected: os.Open resolves path (including a
// symlink) to the underlying file, and every check below (permissions, then
// content) is performed against that opened, resolved file — never against
// the symlink itself. This is intentional: Docker secrets and Kubernetes
// secret volumes commonly present a keyring file as a symlink into a
// separately-mounted, read-only tmpfs, and rejecting that shape outright
// would break a legitimate secret mount without adding any real protection,
// since the security-relevant properties (contents, permissions) belong to
// the resolved target, not the symlink.
//
// The resolved file's mode is checked for group/world-writable bits (see
// insecureFileModeMask); a match is rejected. This package never changes a
// file's permissions (no chmod) and never creates a keyring file — there is
// no default or fallback keyring. An operator whose mount does not meet this
// bar must fix the mount; this package will not paper over it.
func LoadKeyringFile(path string) (*Keyring, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: unable to open keyring file", ErrInvalidKeyring)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: unable to stat keyring file", ErrInvalidKeyring)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: keyring path is a directory", ErrInvalidKeyring)
	}
	if info.Mode().Perm()&insecureFileModeMask != 0 {
		return nil, fmt.Errorf("%w: keyring file permissions allow group or world write", ErrInvalidKeyring)
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("%w: unable to read keyring file", ErrInvalidKeyring)
	}

	return ParseKeyringJSON(data)
}
