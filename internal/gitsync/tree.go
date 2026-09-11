package gitsync

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"

	"github.com/alternayte/sluice/internal/flow"
)

// collect reads the regular files below prefix of a tree from the object store. It never
// writes repository content to a file system. Symlinks, submodules and paths that fail
// flow.ValidPath are skipped with a warning (REQ-NS-005, SI-08).
func collect(st storer.EncodedObjectStorer, root *object.Tree, prefix string, maxFile int64) ([]File, []string, error) {
	tree := root
	prefix = strings.Trim(prefix, "/")
	if prefix != "" {
		sub, err := root.Tree(prefix)
		if err != nil {
			return nil, nil, fmt.Errorf("repo path %q is not a directory at this commit", prefix)
		}
		tree = sub
	}
	var files []File
	var warnings []string
	var walk func(t *object.Tree, base string) error
	walk = func(t *object.Tree, base string) error {
		for _, e := range t.Entries {
			p := e.Name
			if base != "" {
				p = base + "/" + e.Name
			}
			shown := p
			if prefix != "" {
				shown = prefix + "/" + p
			}
			if err := flow.ValidPath(p); err != nil {
				warnings = append(warnings, fmt.Sprintf("skipped %s: %v", shown, err))
				continue
			}
			switch e.Mode {
			case filemode.Dir:
				sub, err := object.GetTree(st, e.Hash)
				if err != nil {
					return err
				}
				if err := walk(sub, p); err != nil {
					return err
				}
			case filemode.Symlink:
				warnings = append(warnings, "skipped symlink "+shown)
			case filemode.Submodule:
				warnings = append(warnings, "skipped submodule "+shown)
			case filemode.Regular, filemode.Executable, filemode.Deprecated:
				blob, err := object.GetBlob(st, e.Hash)
				if err != nil {
					return err
				}
				if blob.Size > maxFile {
					return fmt.Errorf("%s has %d bytes, the limit is %d", shown, blob.Size, maxFile)
				}
				r, err := blob.Reader()
				if err != nil {
					return err
				}
				b, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil {
					return err
				}
				files = append(files, File{Path: p, Content: b, Executable: e.Mode == filemode.Executable})
			default:
				warnings = append(warnings, "skipped "+shown+": unsupported file mode")
			}
		}
		return nil
	}
	if err := walk(tree, ""); err != nil {
		return nil, nil, err
	}
	return files, warnings, nil
}

// entry is one leaf of a flattened tree: a blob, symlink or submodule with its mode.
type entry struct {
	mode filemode.FileMode
	hash plumbing.Hash
}

// flatten returns all leaves of a tree by their raw path. Names are joined with "/"
// without cleaning, so that unusual entries stay as they are.
func flatten(st storer.EncodedObjectStorer, t *object.Tree, base string, out map[string]entry) error {
	for _, e := range t.Entries {
		p := e.Name
		if base != "" {
			p = base + "/" + e.Name
		}
		if e.Mode == filemode.Dir {
			sub, err := object.GetTree(st, e.Hash)
			if err != nil {
				return err
			}
			if err := flatten(st, sub, p, out); err != nil {
				return err
			}
			continue
		}
		out[p] = entry{mode: e.Mode, hash: e.Hash}
	}
	return nil
}

// writeBlob stores content and returns its hash.
func writeBlob(st storer.EncodedObjectStorer, content []byte) (plumbing.Hash, error) {
	obj := st.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err := w.Write(content); err != nil {
		return plumbing.ZeroHash, err
	}
	if err := w.Close(); err != nil {
		return plumbing.ZeroHash, err
	}
	return st.SetEncodedObject(obj)
}

// buildTree writes nested trees for the flattened leaves and returns the root tree hash.
func buildTree(st storer.EncodedObjectStorer, leaves map[string]entry) (plumbing.Hash, error) {
	type node struct {
		children map[string]*node
		leaf     *entry
	}
	root := &node{children: map[string]*node{}}
	for p, e := range leaves {
		parts := strings.Split(p, "/")
		n := root
		for _, part := range parts[:len(parts)-1] {
			c, ok := n.children[part]
			if !ok || c.leaf != nil {
				c = &node{children: map[string]*node{}}
				n.children[part] = c
			}
			n = c
		}
		leaf := e
		n.children[parts[len(parts)-1]] = &node{leaf: &leaf}
	}
	var write func(n *node) (plumbing.Hash, error)
	write = func(n *node) (plumbing.Hash, error) {
		t := &object.Tree{}
		for name, c := range n.children {
			if c.leaf != nil {
				t.Entries = append(t.Entries, object.TreeEntry{Name: name, Mode: c.leaf.mode, Hash: c.leaf.hash})
				continue
			}
			h, err := write(c)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			t.Entries = append(t.Entries, object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: h})
		}
		// Git sorts tree entries by name, with a "/" after directory names.
		key := func(e object.TreeEntry) string {
			if e.Mode == filemode.Dir {
				return e.Name + "/"
			}
			return e.Name
		}
		sort.Slice(t.Entries, func(i, j int) bool { return key(t.Entries[i]) < key(t.Entries[j]) })
		obj := st.NewEncodedObject()
		if err := t.Encode(obj); err != nil {
			return plumbing.ZeroHash, err
		}
		return st.SetEncodedObject(obj)
	}
	return write(root)
}
