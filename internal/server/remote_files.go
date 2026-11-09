package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"tuxee/internal/files"
	"tuxee/internal/i18n"
	"tuxee/internal/identity"
	"tuxee/internal/remotes"
)

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// remoteFileOperation handles core file-manager operations over SSH.
// Trash, archives, editor staging and recursive size remain local-only in v1
// and return the existing invalid-operation error.
func remoteFileOperation(ctx context.Context, user identity.User, remote *remotes.Remote, input files.Request) (files.Response, io.Reader, error) {
	switch input.Operation {
	case "list", "stat", "inspect":
		return remoteFileMeta(ctx, user, remote, input)
	case "mkdir", "delete", "chmod", "rename", "move", "copy":
		return remoteFileMutate(ctx, user, remote, input)
	case "read", "download":
		return remoteFileRead(ctx, user, remote, input)
	case "write", "upload":
		return files.Response{}, nil, errRemoteUnsupported()
	default:
		return files.Response{}, nil, errRemoteUnsupported()
	}
}

func errRemoteUnsupported() error {
	return i18n.New("backend.server.invalidFileOperation")
}

func remoteFileMeta(ctx context.Context, user identity.User, remote *remotes.Remote, input files.Request) (files.Response, io.Reader, error) {
	if input.Operation == "list" {
		entries, err := remoteListDir(ctx, user, remote, input.Path)
		if err != nil {
			return files.Response{}, nil, err
		}
		return files.Response{Path: filepath.Clean(input.Path), Entries: entries}, nil, nil
	}
	entry, err := remoteStat(ctx, user, remote, input.Path)
	if err != nil {
		return files.Response{}, nil, err
	}
	return files.Response{Path: entry.Path, Entry: &entry}, nil, nil
}

func remoteStat(ctx context.Context, user identity.User, remote *remotes.Remote, p string) (files.Entry, error) {
	if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
		return files.Entry{}, i18n.New("backend.server.invalidRequestBody")
	}
	cmd := fmt.Sprintf("stat -c '%%n\\x1f%%F\\x1f%%s\\x1f%%a\\x1f%%u\\x1f%%g\\x1f%%Y\\x1f%%X\\x1f%%Z' -- %s && readlink -- %s; echo READLINK:$?", shellQuote(p), shellQuote(p))
	out, err := remotes.Exec(ctx, user.Command, remote, cmd)
	if err != nil {
		return files.Entry{}, err
	}
	line := strings.SplitN(out, "\n", 2)[0]
	parts := strings.Split(line, "\x1f")
	if len(parts) < 9 {
		return files.Entry{}, fmt.Errorf("cannot stat remote path")
	}
	size, _ := strconv.ParseInt(parts[2], 10, 64)
	uid64, _ := strconv.ParseUint(parts[4], 10, 32)
	gid64, _ := strconv.ParseUint(parts[5], 10, 32)
	mtime, _ := strconv.ParseInt(strings.Split(parts[6], ".")[0], 10, 64)
	atime, _ := strconv.ParseInt(strings.Split(parts[7], ".")[0], 10, 64)
	ctime, _ := strconv.ParseInt(strings.Split(parts[8], ".")[0], 10, 64)
	kind := parts[1]
	entry := files.Entry{
		Name: path.Base(strings.TrimSuffix(p, "/")), Path: filepath.Clean(p),
		Directory: kind == "directory", Size: size,
		Mode: fmt.Sprintf("%04s", parts[3]), UID: uint32(uid64), GID: uint32(gid64),
		Modified: time.Unix(mtime, 0), Accessed: time.Unix(atime, 0), Changed: time.Unix(ctime, 0),
	}
	if kind == "symbolic link" {
		entry.Symlink = true
		if idx := strings.Index(out, "\n"); idx >= 0 {
			rest := strings.Split(out[idx+1:], "READLINK:")[0]
			entry.Link = strings.TrimSpace(strings.Split(rest, "\n")[0])
		}
	}
	if p == "/" {
		entry.Name = "/"
	}
	return entry, nil
}

func remoteListDir(ctx context.Context, user identity.User, remote *remotes.Remote, dir string) ([]files.Entry, error) {
	if !filepath.IsAbs(dir) || strings.ContainsRune(dir, 0) {
		return nil, i18n.New("backend.server.invalidRequestBody")
	}
	cmd := fmt.Sprintf("find %s -mindepth 1 -maxdepth 1 -printf '%%p\\x1f%%y\\x1f%%s\\x1f%%m\\x1f%%u\\x1f%%g\\x1f%%T@\\x1f%%A@\\x1f%%C@\\x1f%%l\\n' 2>&1", shellQuote(dir))
	out, err := remotes.Exec(ctx, user.Command, remote, cmd)
	if err != nil {
		return nil, err
	}
	var entries []files.Entry
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 10)
		if len(parts) < 9 {
			continue
		}
		full := parts[0]
		if !filepath.IsAbs(full) {
			continue
		}
		size, _ := strconv.ParseInt(parts[2], 10, 64)
		uid64, _ := strconv.ParseUint(parts[4], 10, 32)
		gid64, _ := strconv.ParseUint(parts[5], 10, 32)
		mtime := parseFloatTime(parts[6])
		atime := parseFloatTime(parts[7])
		ctime := parseFloatTime(parts[8])
		link := ""
		if len(parts) == 10 {
			link = parts[9]
		}
		kind := parts[1]
		entry := files.Entry{
			Name: path.Base(full), Path: full,
			Directory: kind == "d", Symlink: kind == "l", Size: size,
			Mode: fmt.Sprintf("%04s", parts[3]), UID: uint32(uid64), GID: uint32(gid64),
			Modified: mtime, Accessed: atime, Changed: ctime, Link: link,
		}
		entries = append(entries, entry)
		if len(entries) >= 10000 {
			break
		}
	}
	return entries, nil
}

func parseFloatTime(s string) time.Time {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(int64(f), int64((f-float64(int64(f)))*1e9))
}

func remoteFileMutate(ctx context.Context, user identity.User, remote *remotes.Remote, input files.Request) (files.Response, io.Reader, error) {
	var cmd string
	switch input.Operation {
	case "mkdir":
		cmd = fmt.Sprintf("mkdir -p -- %s", shellQuote(input.Path))
		if _, err := remotes.Exec(ctx, user.Command, remote, cmd); err != nil {
			return files.Response{}, nil, err
		}
		return files.Response{Path: filepath.Clean(input.Path)}, nil, nil
	case "delete":
		if input.Path == "/" {
			return files.Response{}, nil, i18n.New("backend.server.invalidFileOperation")
		}
		cmd = fmt.Sprintf("rm -rf -- %s", shellQuote(input.Path))
		if _, err := remotes.Exec(ctx, user.Command, remote, cmd); err != nil {
			return files.Response{}, nil, err
		}
		return files.Response{}, nil, nil
	case "chmod":
		if _, err := strconv.ParseUint(input.Mode, 8, 12); err != nil {
			return files.Response{}, nil, err
		}
		cmd = fmt.Sprintf("chmod %s -- %s", input.Mode, shellQuote(input.Path))
		if _, err := remotes.Exec(ctx, user.Command, remote, cmd); err != nil {
			return files.Response{}, nil, err
		}
		return files.Response{}, nil, nil
	case "rename", "move":
		cmd = fmt.Sprintf("mv -- %s %s", shellQuote(input.Path), shellQuote(input.Destination))
		if _, err := remotes.Exec(ctx, user.Command, remote, cmd); err != nil {
			return files.Response{}, nil, err
		}
		return files.Response{Path: filepath.Clean(input.Destination)}, nil, nil
	case "copy":
		cmd = fmt.Sprintf("cp -a -- %s %s", shellQuote(input.Path), shellQuote(input.Destination))
		if _, err := remotes.Exec(ctx, user.Command, remote, cmd); err != nil {
			return files.Response{}, nil, err
		}
		return files.Response{Path: filepath.Clean(input.Destination)}, nil, nil
	}
	return files.Response{}, nil, errRemoteUnsupported()
}

func remoteFileRead(ctx context.Context, user identity.User, remote *remotes.Remote, input files.Request) (files.Response, io.Reader, error) {
	// V1 streams whole regular files base64-encoded and bounded, matching the
	// local worker's 6 MiB text limit for editor reads.
	out, err := remotes.Exec(ctx, user.Command, remote, fmt.Sprintf("base64 -w0 -- %s", shellQuote(input.Path)))
	if err != nil {
		return files.Response{}, nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
	if err != nil {
		return files.Response{}, nil, err
	}
	if input.Operation == "download" || input.Operation == "inspect" {
		entry, err := remoteStat(ctx, user, remote, input.Path)
		if err != nil {
			return files.Response{}, nil, err
		}
		if input.Operation == "inspect" {
			return files.Response{Path: entry.Path, Entry: &entry}, nil, nil
		}
		return files.Response{Path: entry.Path, Entry: &entry}, &bytesReader{data: raw}, nil
	}
	return files.Response{Path: filepath.Clean(input.Path), Content: string(raw)}, nil, nil
}

type bytesReader struct {
	data []byte
	off  int
}

func (b *bytesReader) Read(p []byte) (int, error) {
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

func remoteDownloadStream(ctx context.Context, user identity.User, remote *remotes.Remote, p string) (io.ReadCloser, files.Entry, error) {
	entry, err := remoteStat(ctx, user, remote, p)
	if err != nil {
		return nil, files.Entry{}, err
	}
	if entry.Directory {
		return nil, files.Entry{}, i18n.New("backend.server.invalidFileOperation")
	}
	bin, args := remotes.ExecArgs(remote, "cat -- "+shellQuote(p))
	cmd := user.Command(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, files.Entry{}, err
	}
	if err := cmd.Start(); err != nil {
		return nil, files.Entry{}, err
	}
	return &remoteCmdReader{cmd: cmd, Reader: stdout, entry: entry}, entry, nil
}

type remoteCmdReader struct {
	cmd *exec.Cmd
	io.Reader
	entry files.Entry
}

func (r *remoteCmdReader) Close() error {
	err := r.cmd.Wait()
	if c, ok := r.Reader.(io.Closer); ok {
		_ = c.Close()
	}
	return err
}

func remoteUploadStream(ctx context.Context, user identity.User, remote *remotes.Remote, dest string, size int64, body io.Reader) (string, error) {
	if size < 0 {
		return "", i18n.New("backend.server.invalidRequestBody")
	}
	tmp := dest + ".tuxee-upload-part"
	bin, args := remotes.ExecArgs(remote, "cat > "+shellQuote(tmp)+" && mv -- "+shellQuote(tmp)+" "+shellQuote(dest))
	cmd := user.Command(ctx, bin, args...)
	cmd.Stdin = io.LimitReader(body, size+1)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if out, err := cmd.Output(); err != nil {
		_ = out
		detail := stderr.String()
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("%s", detail)
	} else {
		_ = out
	}
	return dest, nil
}
