package buffer

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"github.com/minio/sio"
	"github.com/pkg/errors"
)

const (
	// DefaultMaxMemorySize is used when Buffer is created with NewBuffer() or NewBufferString()
	DefaultMaxMemorySize = 2 << 20 // 2 MB
)

// ErrBufferFinished is used when Buffer.Write() method is called after Buffer.Read()
var ErrBufferFinished = errors.New("buffer is finished")

// Buffer is a buffer which can store data on a disk. It isn't thread-safe!
type Buffer struct {
	maxInMemorySize int

	writingFinished bool
	readingFinished bool

	size   int
	offset int

	// tempFileDir is a directory for temp files. It is empty by default (so, "ioutil.TempFile" uses os.TempDir)
	tempFileDir string

	encrypt       bool
	encryptionKey [32]byte

	// buff is used to store data in memory
	buff bytes.Buffer

	// writeFile is used to write the data on a disk
	writeFile io.WriteCloser
	// readFile is used to read the data from a disk
	readFile io.ReadCloser

	useFile  bool
	filename string
}

// NewBufferWithMaxMemorySize creates a new Buffer with passed maxInMemorySize
func NewBufferWithMaxMemorySize(maxInMemorySize int) *Buffer {
	b := &Buffer{
		maxInMemorySize: maxInMemorySize,
	}

	// Grow the internal buffer
	// TODO: should we use just maxInMemorySize?
	b.buff.Grow(maxInMemorySize / 2)

	return b
}

// NewBuffer creates a new Buffer with DefaultMaxMemorySize and calls Write(buf).
// If an error occurred, it panics
func NewBuffer(buf []byte) *Buffer {
	b := NewBufferWithMaxMemorySize(DefaultMaxMemorySize)
	if buf == nil || len(buf) == 0 {
		// A special case
		return b
	}

	_, err := b.Write(buf)
	if err != nil {
		panic(err)
	}

	return b
}

// NewBufferString calls NewBuffer([]byte(s))
func NewBufferString(s string) *Buffer {
	return NewBuffer([]byte(s))
}

// ChangeTempDir changes directory for temp files
func (b *Buffer) ChangeTempDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return errors.Wrapf(err, "can't open directory '%s'", dir)
	}
	defer f.Close()

	stats, err := f.Stat()
	if err != nil {
		return errors.Wrapf(err, "can't get stats of the directory '%s'", dir)
	}
	if !stats.IsDir() {
		return errors.Errorf("'%s' is not a directory", dir)
	}

	path, err := filepath.Abs(dir)
	if err != nil {
		return errors.New("can't get an absolute path")
	}

	// Change
	b.tempFileDir = path

	return nil
}

// EnableEncryption enables encryption and generates an encryption key
func (b *Buffer) EnableEncryption() error {
	b.encrypt = true

	key := make([]byte, len(b.encryptionKey))
	_, err := rand.Read(key)
	if err != nil {
		return errors.Wrap(err, "can't read random data")
	}

	for i := range key {
		b.encryptionKey[i] = key[i]
	}

	return nil
}

// Write writes data into bytes.Buffer while size of the Buffer is less than maxInMemorySize, when size of Buffer is equal to maxInMemorySize, Write creates a temporary file and writes remaining data into this one.
// Write returns ErrBufferFinished after the call of Buffer.Read(), Buffer.ReadByte() or Buffer.Next()
func (b *Buffer) Write(data []byte) (n int, err error) {
	if b.writingFinished {
		return 0, ErrBufferFinished
	}

	defer func() {
		b.size += n
	}()

	if !b.useFile {
		if b.buff.Len()+len(data) <= b.maxInMemorySize {
			// Just write data into the buffer
			n, err = b.buff.Write(data)
			return
		}

		// We have to use a file. But fill the buffer at first

		bound := b.maxInMemorySize - b.buff.Len()
		n, err = b.buff.Write(data[:bound])
		if err != nil {
			return
		}

		// Trim written bytes
		data = data[bound:]

		b.useFile = true

		// Create a temporary file
		file, err := ioutil.TempFile(b.tempFileDir, "go-disk-buffer-*.tmp")
		if err != nil {
			return n, errors.Wrap(err, "can't create a temp file")
		}

		var writeFile io.WriteCloser = file
		if b.encrypt {
			writeFile, err = sio.EncryptWriter(file, sio.Config{Key: b.encryptionKey[:]})
			if err != nil {
				return n, errors.Wrap(err, "can't create an encryption stream")
			}
		}
		b.writeFile = writeFile
		b.filename = file.Name()

		// fallthrough
	}

	// Write data into the file
	n1, err := b.writeFile.Write(data)
	n += n1
	return
}

// WriteByte writes a single byte.
//
// It uses Buffer.Write underhood
func (b *Buffer) WriteByte(c byte) error {
	slice := []byte{c}
	_, err := b.Write(slice)
	return err
}

// WriteRune writes a rune.
//
// It uses bytes.Buffer and Buffer.Write underhood.
func (b *Buffer) WriteRune(r rune) (n int, err error) {
	tmp := bytes.Buffer{}
	n, err = tmp.WriteRune(r)
	if err != nil {
		return n, err
	}

	return b.Write(tmp.Bytes())
}

// WriteString writes a string
func (b *Buffer) WriteString(s string) (n int, err error) {
	return b.Write([]byte(s))
}

// ReadFrom reads data from r until EOF and writes it into the Buffer.
func (b *Buffer) ReadFrom(r io.Reader) (int64, error) {
	var n int64

	data := make([]byte, 512)
	for {
		rN, rErr := r.Read(data)
		if rErr != nil && rErr != io.EOF {
			return n, errors.Wrap(rErr, "can't read data from passed io.Reader")
		}

		data = data[:rN]
		wN, wErr := b.Write(data)
		if wErr != nil {
			return n + int64(wN), errors.Wrap(wErr, "can't write data")
		}
		n += int64(rN)

		if rErr == io.EOF {
			return n, nil
		}

		data = data[:cap(data)]
	}
}

// Read reads data from bytes.Buffer or from a file. A temp file is deleted when Read() encounter n == 0
func (b *Buffer) Read(data []byte) (n int, err error) {
	if b.readingFinished {
		return 0, io.EOF
	}

	if !b.writingFinished {
		// Finish writing and close Write&Read file if needed
		if b.writeFile != nil {
			b.writeFile.Close()
			b.writeFile = nil
		}
		b.writingFinished = true
	}

	// Check if reading is finished
	defer func() {
		b.offset += n

		// If n is less than size of data slice, reading is finished
		if n < len(data) {
			b.readingFinished = true
		}

		if b.readingFinished && b.readFile != nil {
			// Can close the file
			b.readFile.Close()
			os.Remove(b.filename)

			b.readFile = nil
			b.filename = ""
		}
	}()

	if b.buff.Len() != 0 {
		// Use the buffer
		n, err = b.readFromBuffer(data)
		if err != nil || n == len(data) || !b.useFile {
			// Return if got an error, we filled the slice with data from buffer or we don't use a file
			return
		}

		// Can use the file to fill the slice

		var n1 int

		temp := make([]byte, len(data)-n)
		n1, err = b.readFromFile(temp)
		temp = temp[:n1]
		copy(data[n:], temp)

		n += n1
		return
	}

	if b.useFile {
		// Use the file
		n, err = b.readFromFile(data)
		return
	}

	// Reaching this code means that we buffer is empty and we don't use a file. So, reading is finished

	n = 0
	err = io.EOF
	return
}

func (b *Buffer) ReadAt(data []byte, off int64) (n int, err error) {
	// Input validation
	if off < 0 {
		return 0, fmt.Errorf("negative offset: %d", off)
	}
	if len(data) == 0 {
		return 0, nil
	}
	if off >= int64(b.size) {
		return 0, io.EOF
	}

	// Ensure writing is finished before reading
	if !b.writingFinished {
		if b.writeFile != nil {
			b.writeFile.Close()
			b.writeFile = nil
		}
		b.writingFinished = true
	}

	bufferSize := b.buff.Len()
	totalBytesToRead := len(data)
	bytesRead := 0

	// Case 1: Read starts and ends within buffer
	if off < int64(bufferSize) {
		// Calculate how much we can read from buffer
		bufferBytesToRead := bufferSize - int(off)
		if bufferBytesToRead > totalBytesToRead {
			bufferBytesToRead = totalBytesToRead
		}

		// Read from buffer
		bufferReader := bytes.NewReader(b.buff.Bytes())
		n, err := bufferReader.ReadAt(data[:bufferBytesToRead], off)
		if err != nil && err != io.EOF {
			return n, err
		}
		bytesRead += n
		data = data[n:]
		off += int64(n)
	}

	// Case 2: Read from file if there's more data needed and we use a file
	if len(data) > 0 && b.useFile {
		// Open file if not already open
		if b.readFile == nil {
			file, err := os.Open(b.filename)
			if err != nil {
				return bytesRead, errors.Wrapf(err, "can't open a temp file '%s'", b.filename)
			}

			var readFile io.ReadCloser = file
			if b.encrypt {
				reader, err := sio.DecryptReaderAt(file, sio.Config{Key: b.encryptionKey[:]})
				if err != nil {
					return bytesRead, errors.Wrap(err, "can't create a decryption stream")
				}
				readFile = newSioDecryptReaderAtWrapper(reader, file)
			}
			b.readFile = readFile
		}

		// Read from file
		if readerAt, ok := b.readFile.(io.ReaderAt); ok {
			fileOffset := off - int64(bufferSize)
			n, err := readerAt.ReadAt(data, fileOffset)
			bytesRead += n
			if err != nil && err != io.EOF {
				return bytesRead, err
			}
		} else {
			return bytesRead, fmt.Errorf("readFile does not implement io.ReaderAt")
		}
	}

	// Return EOF if we've read less than requested (end of buffer/file)
	if bytesRead < totalBytesToRead {
		return bytesRead, io.EOF
	}

	return bytesRead, nil
}

func (b *Buffer) readFromBuffer(data []byte) (n int, err error) {
	return b.buff.Read(data)
}

func (b *Buffer) readFromFile(data []byte) (n int, err error) {
	if b.readFile == nil {
		file, err := os.Open(b.filename)
		if err != nil {
			return 0, errors.Wrapf(err, "can't open a temp file '%s'", b.filename)
		}

		var readFile io.ReadCloser = file
		if b.encrypt {
			reader, err := sio.DecryptReader(file, sio.Config{Key: b.encryptionKey[:]})
			if err != nil {
				return 0, errors.Wrap(err, "can't create a decryption stream")
			}
			readFile = newSioDecryptReaderWrapper(reader, file)
		}

		b.readFile = readFile
	}

	return b.readFile.Read(data)
}

// ReadByte reads a single byte.
//
// It uses Buffer.Read underhood
func (b *Buffer) ReadByte() (byte, error) {
	c := make([]byte, 1)
	_, err := b.Read(c)
	return c[0], err
}

// ReadBytes reads until the first occurrence of delim in the input,
// returning a slice containing the data up to and including the delimiter.
// If ReadBytes encounters an error before finding a delimiter,
// it returns the data read before the error and the error itself (often io.EOF).
func (b *Buffer) ReadBytes(delim byte) ([]byte, error) {
	var result []byte

	for {
		c, err := b.ReadByte()
		if err != nil {
			return result, err
		}

		result = append(result, c)

		if c == delim {
			return result, nil
		}
	}
}

// ReadString reads until the first occurrence of delim in the input,
// returning a string containing the data up to and including the delimiter.
// If ReadString encounters an error before finding a delimiter,
// it returns the data read before the error and the error itself (often io.EOF).
func (b *Buffer) ReadString(delim byte) (string, error) {
	bytes, err := b.ReadBytes(delim)
	return string(bytes), err
}

// ReadRune reads a single UTF-8 encoded Unicode character and returns the
// rune and its size in bytes. If the encoded rune is invalid, it consumes
// one byte and returns unicode.ReplacementChar (U+FFFD) with a size of 1.
func (b *Buffer) ReadRune() (r rune, size int, err error) {
	var p []byte

	for {
		c, err := b.ReadByte()
		if err != nil {
			return r, 0, err
		}

		p = append(p, c)

		if utf8.FullRune(p) {
			r, size = utf8.DecodeRune(p)
			return r, size, nil
		}
	}
}

// Next returns a slice containing the next n bytes from the buffer.
// If an error occurred, it panics
func (b *Buffer) Next(n int) []byte {
	slice := make([]byte, n)
	n, err := b.buff.Read(slice)
	if err != nil {
		panic(err)
	}
	slice = slice[:n]
	return slice
}

// WriteTo writes data to w until the buffer is drained or an error occurs.
func (b *Buffer) WriteTo(w io.Writer) (int64, error) {
	var n int64

	data := make([]byte, 512)
	for {
		rN, rErr := b.Read(data)
		if rErr != nil && rErr != io.EOF {
			return n, errors.Wrap(rErr, "can't read data from Buffer")
		}

		data = data[:rN]
		wN, wErr := w.Write(data)
		if wErr != nil {
			return n + int64(wN), errors.Wrap(wErr, "can't write data into io.Writer")
		}
		n += int64(rN)

		if rErr == io.EOF {
			return n, nil
		}

		data = data[:cap(data)]
	}
}

// Len returns the number of bytes of the unread portion of the buffer
func (b *Buffer) Len() int {
	return b.size - b.offset
}

// Cap is equal to Buffer.Len()
func (b *Buffer) Cap() int {
	return b.Len()
}

// Reset resets buffer and remove file if needed
func (b *Buffer) Reset() {
	b.buff.Reset()

	if b.writeFile != nil {
		b.writeFile.Close()
	}
	if b.readFile != nil {
		b.readFile.Close()
	}

	if b.filename != "" {
		os.Remove(b.filename)
	}

	b.writingFinished = false
	b.readingFinished = false
	b.writeFile = nil
	b.readFile = nil
	b.useFile = false
	b.filename = ""
}

// sioDecryptReaderWrapper is a wrapper for sio.DecryptReader() function
// that satisfy io.ReadCloser.
// It reads from passed io.Reader and closes the original file
type sioDecryptReaderWrapper struct {
	r            io.Reader
	originalFile *os.File
}

func newSioDecryptReaderWrapper(r io.Reader, file *os.File) *sioDecryptReaderWrapper {
	return &sioDecryptReaderWrapper{
		r:            r,
		originalFile: file,
	}
}

func (rw *sioDecryptReaderWrapper) Read(p []byte) (int, error) {
	return rw.r.Read(p)
}

func (rw *sioDecryptReaderWrapper) Close() error {
	return rw.originalFile.Close()
}

// sioDecryptReaderAtWrapper is a wrapper for sio.DecryptReaderAt() function
// that satisfies io.ReadCloser and io.ReaderAt.
// It reads from passed io.ReaderAt and closes the original file
type sioDecryptReaderAtWrapper struct {
	r            io.ReaderAt
	originalFile *os.File
	offset       int64      // Current read position for sequential Read() calls
	mu           sync.Mutex // Mutex to protect offset for thread safety
}

func newSioDecryptReaderAtWrapper(r io.ReaderAt, file *os.File) *sioDecryptReaderAtWrapper {
	return &sioDecryptReaderAtWrapper{
		r:            r,
		originalFile: file,
	}
}

func (rw *sioDecryptReaderAtWrapper) Read(p []byte) (int, error) {
	// Implement sequential reading using ReadAt with internal offset
	rw.mu.Lock()
	defer rw.mu.Unlock()

	n, err := rw.r.ReadAt(p, rw.offset)
	rw.offset += int64(n)
	return n, err
}

func (rw *sioDecryptReaderAtWrapper) ReadAt(b []byte, off int64) (n int, err error) {
	return rw.r.ReadAt(b, off)
}

func (rw *sioDecryptReaderAtWrapper) Close() error {
	return rw.originalFile.Close()
}
