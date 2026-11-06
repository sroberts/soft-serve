package web

import (
	"bufio"
	"bytes"
	"io"
	gopath "path"
	"path/filepath"
	"strconv"

	"github.com/charmbracelet/soft-serve/git"
	"github.com/charmbracelet/soft-serve/pkg/lfs"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/storage"
)

// Files tracked by Git LFS are committed as small pointers; the content is
// in Soft Serve's LFS store, where the LFS endpoints put it. The views show
// and serve that content instead of the pointer.

// lfsPointerMax is the largest blob that can be an LFS pointer.
const lfsPointerMax = 1024

// lfsFile is a tree entry's LFS content: the pointer, and the object when
// the server has it (nil when it doesn't).
type lfsFile struct {
	pointer lfs.Pointer
	object  storage.Object
}

// lfsLookup reports whether te is an LFS pointer and opens its object. The
// caller closes the object. It returns nil for an ordinary file.
func (b *browser) lfsLookup(r proto.Repository, te *git.TreeEntry) *lfsFile {
	if !te.IsBlob() || te.Size() >= lfsPointerMax {
		return nil
	}
	buf, err := te.Contents()
	if err != nil {
		return nil
	}
	p, err := lfs.ReadPointerFromBuffer(buf)
	if err != nil || !p.IsValid() {
		return nil
	}
	// Each repository has its own store, so a pointer can only reach
	// objects pushed to this repository.
	strg := storage.NewLocalStorage(filepath.Join(b.cfg.DataPath, "lfs", strconv.FormatInt(r.ID(), 10)))
	obj, err := strg.Open(gopath.Join("objects", p.RelativePath()))
	if err != nil {
		return &lfsFile{pointer: p}
	}
	return &lfsFile{pointer: p, object: obj}
}

// sniff peeks at the start of r to tell text from binary, and returns a
// reader that still yields all of r.
func sniff(r io.Reader) (io.Reader, bool) {
	br := bufio.NewReaderSize(r, 8000)
	head, _ := br.Peek(8000)
	bin, _ := git.IsBinary(bytes.NewReader(head))
	return br, bin
}
