package lazybuilder

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"github.com/ingenieux/lazybuilder/log"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const twBufSize = 32 * 1024

type (
	Archive       io.ReadCloser
	ArchiveReader io.Reader
	Compression   int
	TarOptions    struct {
		Includes    []string
		Excludes    []string
		Compression Compression
		NoLchown    bool
	}
)

var (
	ErrNotImplemented = errors.New("Function not implemented")
)

const (
	Uncompressed Compression = iota
	Bzip2
	Gzip
	Xz
)

func IsArchive(header []byte) bool {
	compression := DetectCompression(header)
	if compression != Uncompressed {
		return true
	}
	r := tar.NewReader(bytes.NewBuffer(header))
	_, err := r.Next()
	return err == nil
}

func DetectCompression(source []byte) Compression {
	for compression, m := range map[Compression][]byte{
		Bzip2: {0x42, 0x5A, 0x68},
		Gzip:  {0x1F, 0x8B, 0x08},
		Xz:    {0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00},
	} {
		if len(source) < len(m) {
			log.Debugf("Len too short")
			continue
		}
		if bytes.Compare(m, source[:len(m)]) == 0 {
			return compression
		}
	}
	return Uncompressed
}

type NopWriter struct{}

func (*NopWriter) Write(buf []byte) (int, error) {
	return len(buf), nil
}

type nopWriteCloser struct {
	io.Writer
}

func (w *nopWriteCloser) Close() error { return nil }

func NopWriteCloser(w io.Writer) io.WriteCloser {
	return &nopWriteCloser{w}
}

func CompressStream(dest io.WriteCloser, compression Compression) (io.WriteCloser, error) {

	switch compression {
	case Uncompressed:
		return NopWriteCloser(dest), nil
	case Gzip:
		return gzip.NewWriter(dest), nil
	case Bzip2, Xz:
		// archive/bzip2 does not support writing, and there is no xz support at all
		// However, this is not a problem as docker only currently generates gzipped tars
		return nil, fmt.Errorf("Unsupported compression format %s", (&compression).Extension())
	default:
		return nil, fmt.Errorf("Unsupported compression format %s", (&compression).Extension())
	}
}

func (compression *Compression) Extension() string {
	switch *compression {
	case Uncompressed:
		return "tar"
	case Bzip2:
		return "tar.bz2"
	case Gzip:
		return "tar.gz"
	case Xz:
		return "tar.xz"
	}
	return ""
}

func addTarFile(path, name string, tw *tar.Writer, twBuf *bufio.Writer) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}

	link := ""
	if fi.Mode()&os.ModeSymlink != 0 {
		if link, err = os.Readlink(path); err != nil {
			return err
		}
	}

	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return err
	}

	if fi.IsDir() && !strings.HasSuffix(name, "/") {
		name = name + "/"
	}

	hdr.Name = name

	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}

	if hdr.Typeflag == tar.TypeReg {
		file, err := os.Open(path)
		if err != nil {
			return err
		}

		twBuf.Reset(tw)
		_, err = io.Copy(twBuf, file)
		file.Close()
		if err != nil {
			return err
		}
		err = twBuf.Flush()
		if err != nil {
			return err
		}
		twBuf.Reset(nil)
	}

	return nil
}

// Tar creates an archive from the directory at `path`, and returns it as a
// stream of bytes.
func Tar(path string, compression Compression) (io.ReadCloser, error) {
	return TarWithOptions(path, &TarOptions{Compression: compression})
}

func escapeName(name string) string {
	escaped := make([]byte, 0)
	for i, c := range []byte(name) {
		if i == 0 && c == '/' {
			continue
		}
		// all printable chars except "-" which is 0x2d
		if (0x20 <= c && c <= 0x7E) && c != 0x2d {
			escaped = append(escaped, c)
		} else {
			escaped = append(escaped, fmt.Sprintf("\\%03o", c)...)
		}
	}
	return string(escaped)
}

// Matches returns true if relFilePath matches any of the patterns
func matches(relFilePath string, patterns []string) (bool, error) {
	for _, exclude := range patterns {
		matched, err := filepath.Match(exclude, relFilePath)
		if err != nil {
			log.Errorf("Error matching: %s (pattern: %s)", relFilePath, exclude)
			return false, err
		}
		if matched {
			if filepath.Clean(relFilePath) == "." {
				log.Errorf("Can't exclude whole path, excluding pattern: %s", exclude)
				continue
			}
			log.Debugf("Skipping excluded path: %s", relFilePath)
			return true, nil
		}
	}
	return false, nil
}

// TarWithOptions creates an archive from the directory at `path`, only including files whose relative
// paths are included in `options.Includes` (if non-nil) or not in `options.Excludes`.
func TarWithOptions(srcPath string, options *TarOptions) (io.ReadCloser, error) {
	pipeReader, pipeWriter := io.Pipe()

	compressWriter, err := CompressStream(pipeWriter, options.Compression)
	if err != nil {
		return nil, err
	}

	tw := tar.NewWriter(compressWriter)

	go func() {
		// In general we log errors here but ignore them because
		// during e.g. a diff operation the container can continue
		// mutating the filesystem and we can see transient errors
		// from this

		if options.Includes == nil {
			options.Includes = []string{"."}
		}

		twBuf := bufio.NewWriterSize(nil, twBufSize)

		for _, include := range options.Includes {
			filepath.Walk(filepath.Join(srcPath, include), func(filePath string, f os.FileInfo, err error) error {
				if err != nil {
					log.Debugf("Tar: Can't stat file %s to tar: %s", srcPath, err)
					return nil
				}

				relFilePath, err := filepath.Rel(srcPath, filePath)
				if err != nil {
					return nil
				}

				skip, err := matches(relFilePath, options.Excludes)
				if err != nil {
					log.Debugf("Error matching %s", relFilePath, err)
					return err
				}

				if skip {
					if f.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}

				if err := addTarFile(filePath, relFilePath, tw, twBuf); err != nil {
					log.Debugf("Can't add file %s to tar: %s", srcPath, err)
				}
				return nil
			})
		}

		// Make sure to check the error on Close.
		if err := tw.Close(); err != nil {
			log.Debugf("Can't close tar writer: %s", err)
		}
		if err := compressWriter.Close(); err != nil {
			log.Debugf("Can't close compress writer: %s", err)
		}
		if err := pipeWriter.Close(); err != nil {
			log.Debugf("Can't close pipe writer: %s", err)
		}
	}()

	return pipeReader, nil
}

func BuildTar(root string) (io.ReadCloser, error) {
	filename := path.Join(root, "Dockerfile")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil, fmt.Errorf("no Dockerfile found in %s", root)
	}
	var excludes []string
	ignore, err := ioutil.ReadFile(path.Join(root, ".dockerignore"))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Error reading .dockerignore: '%s'", err)
	}
	for _, pattern := range strings.Split(string(ignore), "\n") {
		ok, err := filepath.Match(pattern, "Dockerfile")
		if err != nil {
			return nil, fmt.Errorf("Bad .dockerignore pattern: '%s', error: %s", pattern, err)
		}
		if ok {
			return nil, fmt.Errorf("Dockerfile was excluded by .dockerignore pattern '%s'", pattern)
		}
		excludes = append(excludes, pattern)
	}

	options := &TarOptions{
		Compression: Uncompressed,
		Excludes:    excludes,
	}

	context, err := TarWithOptions(root, options)

	if err != nil {
		return nil, err
	}

	return context, nil
}
