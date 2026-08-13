//go:build darwin

package workflow

import "golang.org/x/sys/unix"

func renameNoReplaceAt(fromDir int, from string, toDir int, to string) error {
	return unix.RenameatxNp(fromDir, from, toDir, to, unix.RENAME_EXCL)
}
