package buffer

import (
	"bytes"
	"io"
	"math/rand"
	"os"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestBuffer_CheckBufferAndFileSize(t *testing.T) {
	tests := []struct {
		maxSize int
		//
		data []byte
		//
		bufferSize int
		fileSize   int
	}{
		{
			maxSize:    15,
			data:       make([]byte, 10),
			bufferSize: 10,
			fileSize:   0,
		},
		{
			maxSize:    15,
			data:       make([]byte, 15),
			bufferSize: 15,
			fileSize:   0,
		},
		{
			maxSize:    15,
			data:       make([]byte, 16),
			bufferSize: 15,
			fileSize:   1,
		},
		{
			maxSize:    15,
			data:       make([]byte, 20),
			bufferSize: 15,
			fileSize:   5,
		},
		{
			maxSize:    20,
			data:       make([]byte, 1<<20),
			bufferSize: 20,
			fileSize:   1<<20 - 20,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run("", func(t *testing.T) {
			t.Parallel()

			require := require.New(t)

			b := NewBufferWithMaxMemorySize(tt.maxSize)
			defer b.Reset()

			n, err := b.Write(tt.data)
			require.Nil(err, "error during Write()")

			// Checks
			require.Equal(len(tt.data), n, "not all data written")

			require.Equal(len(tt.data), b.Len(), "Len() method returned wrong value")

			require.Equal(tt.bufferSize, b.buff.Len(), "buffer contains wrong amount of bytes")

			if len(tt.data) <= tt.maxSize {
				require.Equal("", b.filename, "buffer created excess file")

				// Must skip file checks
				return
			}

			f, err := os.Open(b.filename)
			require.Nilf(err, "can't open file %s", b.filename)
			defer f.Close()

			fileSize := func() int {
				info, err := f.Stat()
				if err != nil {
					return 0
				}

				return int(info.Size())
			}()

			require.Equal(tt.fileSize, fileSize, "buffer contains wrong amount of bytes")
		})

	}
}

func TestBuffer_WriteAndRead(t *testing.T) {
	tests := []struct {
		maxSize       int
		readSliceSize int
		//
		data [][]byte
		//
		res []byte
	}{
		{
			maxSize:       20,
			readSliceSize: 256,
			data: [][]byte{
				[]byte("123"),
				[]byte("456"),
				[]byte("789"),
			},
			res: []byte("123456789"),
		},
		{
			maxSize:       1,
			readSliceSize: 256,
			data: [][]byte{
				[]byte("123"),
				[]byte("456"),
				[]byte("789"),
			},
			res: []byte("123456789"),
		},
		{
			maxSize:       5,
			readSliceSize: 10,
			data: [][]byte{
				[]byte("123"),
				[]byte("456"),
				[]byte("789"),
			},
			res: []byte("123456789"),
		},
		{
			maxSize:       5,
			readSliceSize: 20,
			data: [][]byte{
				[]byte("123"),
				[]byte("456"),
				[]byte("789"),
			},
			res: []byte("123456789"),
		},
		{
			maxSize:       5,
			readSliceSize: 10,
			data: [][]byte{
				[]byte("123"),
				[]byte("456"),
				[]byte("789"),
			},
			res: []byte("123456789"),
		},
		{
			maxSize:       5,
			readSliceSize: 5,
			data: [][]byte{
				[]byte("123"),
				[]byte("456"),
				[]byte("789"),
			},
			res: []byte("123456789"),
		},
		{
			maxSize:       0,
			readSliceSize: 5,
			data: [][]byte{
				[]byte("123"),
				[]byte("456"),
				[]byte("789"),
			},
			res: []byte("123456789"),
		},
		{
			maxSize:       0,
			readSliceSize: 5,
			data:          [][]byte{},
			res:           nil,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run("", func(t *testing.T) {
			t.Parallel()

			require := require.New(t)

			b := NewBufferWithMaxMemorySize(tt.maxSize)
			defer b.Reset()

			var dataWritten int
			for _, d := range tt.data {
				n, err := b.Write(d)
				dataWritten += len(d)

				require.Nil(err, "error during Write()")
				require.Equal(len(d), n, "not all data written")
				require.Equal(dataWritten, b.Len(), "Len() method returned wrong value")
			}

			res := readByChunks(require, b, tt.readSliceSize)
			require.Equalf(tt.res, res, "wrong content was read")

			require.Equal(0, b.Len(), "Buffer must be empty")
		})
	}
}

func TestBuffer_ReadByte(t *testing.T) {
	require := require.New(t)

	data := []byte("1234")

	b := NewBufferWithMaxMemorySize(len(data) / 2)
	defer b.Reset()

	b.Write([]byte(data))

	for i := 0; i < len(data); i++ {
		c, err := b.ReadByte()
		require.Nil(err)
		require.Equal(data[i], c)
	}
}

func TestBuffer_ReadRune(t *testing.T) {
	require := require.New(t)

	data := []byte("Hello | ✓ | 123456 | Привет!")

	b := NewBufferWithMaxMemorySize(len(data) / 2)
	defer b.Reset()

	b.Write([]byte(data))

	for _, rn := range string(data) {
		r, size, err := b.ReadRune()
		require.Nil(err)
		require.Equal(utf8.RuneLen(rn), size)
		require.Equal(rn, r)
	}
}

func TestBuffer_ReadBytes(t *testing.T) {
	tests := []struct {
		name         string
		maxMemSize   int
		data         string
		delimiter    byte
		expected     []string
		expectErrors []bool
	}{
		{
			name:         "Simple newline delimiter - all in memory",
			maxMemSize:   100,
			data:         "line1\nline2\nline3",
			delimiter:    '\n',
			expected:     []string{"line1\n", "line2\n", "line3"},
			expectErrors: []bool{false, false, true}, // EOF on last read
		},
		{
			name:         "Simple newline delimiter - across memory/disk boundary",
			maxMemSize:   8,
			data:         "line1\nline2\nline3",
			delimiter:    '\n',
			expected:     []string{"line1\n", "line2\n", "line3"},
			expectErrors: []bool{false, false, true}, // EOF on last read
		},
		{
			name:         "Semicolon delimiter",
			maxMemSize:   5,
			data:         "field1;field2;field3",
			delimiter:    ';',
			expected:     []string{"field1;", "field2;", "field3"},
			expectErrors: []bool{false, false, true}, // EOF on last read
		},
		{
			name:         "No delimiter found",
			maxMemSize:   100,
			data:         "no delimiter here",
			delimiter:    '\n',
			expected:     []string{"no delimiter here"},
			expectErrors: []bool{true}, // EOF
		},
		{
			name:         "Empty buffer",
			maxMemSize:   100,
			data:         "",
			delimiter:    '\n',
			expected:     []string{""},
			expectErrors: []bool{true}, // EOF
		},
		{
			name:         "Only delimiter",
			maxMemSize:   100,
			data:         "\n",
			delimiter:    '\n',
			expected:     []string{"\n", ""},
			expectErrors: []bool{false, true}, // EOF on second read
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			b := NewBufferWithMaxMemorySize(tt.maxMemSize)
			defer b.Reset()

			_, err := b.Write([]byte(tt.data))
			require.Nil(err)

			for i, expected := range tt.expected {
				result, err := b.ReadBytes(tt.delimiter)

				if tt.expectErrors[i] {
					require.Equal(io.EOF, err)
				} else {
					require.Nil(err)
				}

				require.Equal(expected, string(result))
			}
		})
	}
}

func TestBuffer_ReadString(t *testing.T) {
	tests := []struct {
		name         string
		maxMemSize   int
		data         string
		delimiter    byte
		expected     []string
		expectErrors []bool
	}{
		{
			name:         "Simple newline delimiter - all in memory",
			maxMemSize:   100,
			data:         "line1\nline2\nline3",
			delimiter:    '\n',
			expected:     []string{"line1\n", "line2\n", "line3"},
			expectErrors: []bool{false, false, true}, // EOF on last read
		},
		{
			name:         "Simple newline delimiter - across memory/disk boundary",
			maxMemSize:   8,
			data:         "line1\nline2\nline3",
			delimiter:    '\n',
			expected:     []string{"line1\n", "line2\n", "line3"},
			expectErrors: []bool{false, false, true}, // EOF on last read
		},
		{
			name:         "Unicode content with comma delimiter",
			maxMemSize:   10,
			data:         "Привет,мир,тест",
			delimiter:    ',',
			expected:     []string{"Привет,", "мир,", "тест"},
			expectErrors: []bool{false, false, true}, // EOF on last read
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			b := NewBufferWithMaxMemorySize(tt.maxMemSize)
			defer b.Reset()

			_, err := b.Write([]byte(tt.data))
			require.Nil(err)

			for i, expected := range tt.expected {
				result, err := b.ReadString(tt.delimiter)

				if tt.expectErrors[i] {
					require.Equal(io.EOF, err)
				} else {
					require.Nil(err)
				}

				require.Equal(expected, result)
			}
		})
	}
}

func TestBuffer_Next(t *testing.T) {
	tests := []struct {
		originalData []byte

		readChunk    int
		receivedData []byte
	}{
		{
			originalData: []byte("Hello, world!"),
			readChunk:    0,
			receivedData: []byte{},
		},
		{
			originalData: []byte("Hello, world!"),
			readChunk:    5,
			receivedData: []byte("Hello"),
		},
		{
			originalData: []byte("Hello, world!"),
			readChunk:    13,
			receivedData: []byte("Hello, world!"),
		},
		{
			originalData: []byte("Hello, world!"),
			readChunk:    20,
			receivedData: []byte("Hello, world!"),
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run("", func(t *testing.T) {
			t.Parallel()

			require := require.New(t)

			b := NewBuffer(tt.originalData)
			defer b.Reset()

			data := b.Next(tt.readChunk)
			require.Equal(tt.receivedData, data)
		})
	}
}

func TestBuffer_ReadFrom(t *testing.T) {
	tests := []struct {
		before []byte
		data   []byte
		after  []byte
	}{
		{
			before: []byte("hello"),
			data:   []byte(""),
			after:  []byte("world"),
		},
		{
			before: []byte(""),
			data:   []byte("test"),
			after:  []byte(""),
		},
		{
			before: []byte("hello"),
			data:   []byte(": some test message: "),
			after:  []byte("world"),
		},
		{
			before: []byte("test"),
			data:   []byte(generateRandomString(1000)),
			after:  []byte("!!!"),
		},
		{
			before: []byte(""),
			data:   []byte(generateRandomString(2047)),
			after:  []byte(""),
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run("", func(t *testing.T) {
			t.Parallel()

			fullMsg := append(append(tt.before, tt.data...), tt.after...)

			require := require.New(t)

			b := NewBuffer(nil)
			defer b.Reset()

			// Write before
			n, err := b.Write([]byte(tt.before))
			require.Nil(err)
			require.Equal(len(tt.before), n)

			// Write the data
			buffer := bytes.NewBuffer(nil)
			buffer.Write(tt.data)

			n1, err := b.ReadFrom(buffer)
			require.Nil(err)
			require.Equal(len(tt.data), int(n1))

			// Write after
			n, err = b.Write([]byte(tt.after))
			require.Nil(err)
			require.Equal(len(tt.after), n)

			// Check

			buffData := readByChunks(require, b, 32)
			require.Equal(fullMsg, buffData)
		})
	}
}

func TestBuffer_WriteSmth(t *testing.T) {
	tests := []struct {
		desc  string
		value interface{} // string, byte or rune
		size  int
	}{
		{desc: "write byte", value: byte('t'), size: 1},
		{desc: "write rune (cyrillic)", value: rune('П'), size: 2},
		{desc: "write rune (symbol)", value: rune('✓'), size: 3},
		{desc: "write string", value: "hello", size: 5},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			require := require.New(t)

			b := NewBuffer(nil)
			defer b.Reset()

			switch v := tt.value.(type) {
			case byte:
				err := b.WriteByte(v)
				require.Nil(err)
			case rune:
				n, err := b.WriteRune(v)
				require.Nil(err)
				require.Equal(tt.size, n)
			case string:
				n, err := b.WriteString(v)
				require.Nil(err)
				require.Equal(tt.size, n)
			}
		})
	}
}

func TestBuffer_WriteTo(t *testing.T) {
	tests := []struct {
		data []byte
	}{
		{data: []byte(generateRandomString(1))},
		{data: []byte(generateRandomString(61))},
		{data: []byte(generateRandomString(513))},
		{data: []byte(generateRandomString(2056))},
	}

	for _, tt := range tests {
		tt := tt

		t.Run("", func(t *testing.T) {
			t.Parallel()

			require := require.New(t)

			b := NewBuffer(nil)
			defer b.Reset()

			// Write

			n, err := b.Write(tt.data)
			require.Nil(err)
			require.Equal(len(tt.data), n)

			// WriteTo
			buffer := bytes.NewBuffer(nil)
			n1, err := b.WriteTo(buffer)
			require.Nil(err)
			require.Equal(int64(len(tt.data)), n1)
			require.Equal(tt.data, buffer.Bytes())
		})
	}
}

func TestBuffer_ChangeTempDir(t *testing.T) {
	if os.Getenv("CI_CD") == "true" {
		// There are problems with permission (with GitHub Action, for example)
		t.Skip("skip the test because there are problems with permission")
	}

	t.Run("Existing dir", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)

		var (
			dir          = "./test"
			maxMemory    = 100
			originalData = []byte(generateRandomString(256))
			chunk        = 64
		)

		err := os.MkdirAll(dir, 0755)
		require.Nil(err)

		buf := NewBufferWithMaxMemorySize(maxMemory)
		err = buf.ChangeTempDir(dir)
		require.Nil(err)

		writeByChunks(require, buf, originalData, chunk)
		data := readByChunks(require, buf, chunk)
		require.Equal(originalData, data)

		err = os.RemoveAll(dir)
		require.Nil(err)
	})

	t.Run("Non-existing dir", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)

		dir := "./123"

		buf := NewBuffer(nil)
		err := buf.ChangeTempDir(dir)
		require.NotNil(err)
	})

	t.Run("File", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)

		var (
			dir  = "./test"
			file = "./test/123.txt"
		)

		err := os.MkdirAll(dir, 0755)
		require.Nil(err)

		f, err := os.Create(file)
		require.Nil(err)
		f.Close()

		buf := NewBuffer(nil)
		err = buf.ChangeTempDir(file)
		require.NotNil(err)
	})
}

func TestBuffer_FuzzTest(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	t.Run("Without encryption", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			t.Run("", func(t *testing.T) {
				t.Parallel()

				require := require.New(t)

				var (
					sliceSize      = rand.Intn(1<<10) + 1
					bufferSize     = rand.Intn(sliceSize * 2) // can be zero
					writeChunkSize = rand.Intn(sliceSize) + 1
					readChunkSize  = rand.Intn(sliceSize) + 1
				)

				defer func() {
					// Log only when test is failed
					if t.Failed() {
						t.Logf("sliceSize: %d; bufferSize: %d; writeChunkSize: %d; readChunkSize: %d\n",
							sliceSize, bufferSize, writeChunkSize, readChunkSize)
					}
				}()

				slice := make([]byte, sliceSize)
				for i := range slice {
					slice[i] = byte(rand.Intn(128))
				}

				b := NewBufferWithMaxMemorySize(bufferSize)
				defer b.Reset()

				// Write slice by chunks
				writeByChunks(require, b, slice, writeChunkSize)

				res := readByChunks(require, b, readChunkSize)
				require.Equal(slice, res, "wrong content was read")
			})
		}
	})

	t.Run("With encryption", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			t.Run("", func(t *testing.T) {
				t.Parallel()

				require := require.New(t)

				var (
					sliceSize      = rand.Intn(1<<10) + 1
					bufferSize     = rand.Intn(sliceSize * 2) // can be zero
					writeChunkSize = rand.Intn(sliceSize) + 1
					readChunkSize  = rand.Intn(sliceSize) + 1
				)

				defer func() {
					// Log only when test is failed
					if t.Failed() {
						t.Logf("sliceSize: %d; bufferSize: %d; writeChunkSize: %d; readChunkSize: %d\n",
							sliceSize, bufferSize, writeChunkSize, readChunkSize)
					}
				}()

				slice := make([]byte, sliceSize)
				for i := range slice {
					slice[i] = byte(rand.Intn(128))
				}

				b := NewBufferWithMaxMemorySize(bufferSize)
				err := b.EnableEncryption()
				require.Nil(err)
				defer b.Reset()

				// Write slice by chunks
				writeByChunks(require, b, slice, writeChunkSize)

				res := readByChunks(require, b, readChunkSize)
				require.Equal(slice, res, "wrong content was read")
			})
		}
	})
}

func writeByChunks(require *require.Assertions, b *Buffer, source []byte, chunk int) {
	// Write slice by chunks
	for i := 0; i < len(source); i += chunk {
		bound := i + chunk
		if bound > len(source) {
			bound = len(source)
		}

		_, err := b.Write(source[i:bound])
		require.Nil(err)
		require.Equal(bound, b.Len())
	}
}

func readByChunks(require *require.Assertions, b *Buffer, chunk int) []byte {
	var (
		res      []byte
		dataRead int
		bufSize  = b.Len()
	)

	data := make([]byte, chunk)
	for {
		n, err := b.Read(data)
		dataRead += n
		data = data[:n]
		res = append(res, data...)
		data = data[:cap(data)]

		require.Equal(dataRead, bufSize-b.Len(), "Len() method returned wrong value")

		if err != nil {
			if err == io.EOF {
				break
			}
			require.Nil(err)
		}
	}

	return res
}

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func generateRandomString(length int) string {
	const alphabetSize = len(alphabet)

	filename := make([]byte, 0, length)

	for i := 0; i < length; i++ {
		n := rand.Intn(alphabetSize)
		filename = append(filename, alphabet[n])
	}

	return string(filename)
}

// Benchmarks

func BenchmarkBuffer(b *testing.B) {
	benchs := []struct {
		description    string
		dataSize       int
		maxBufferSize  int
		writeChunkSize int
		readChunkSize  int
	}{
		{
			description:    "Buffer size is greater than data",
			dataSize:       1 << 20, // 1MB
			maxBufferSize:  2 << 20, // 2MB
			writeChunkSize: 1024,
			readChunkSize:  2048,
		},
		{
			description:    "Buffer size is equal to data",
			dataSize:       1 << 20, // 1MB
			maxBufferSize:  1 << 20, // 1MB
			writeChunkSize: 1024,
			readChunkSize:  2048,
		},
		{
			description:    "Buffer size is less than data",
			dataSize:       20 << 20, // 20MB
			maxBufferSize:  1 << 20,  // 1MB
			writeChunkSize: 1024,
			readChunkSize:  2048,
		},
	}

	for _, bench := range benchs {
		b.Run(bench.description, func(b *testing.B) {
			slice := make([]byte, bench.dataSize)
			for i := range slice {
				slice[i] = byte(rand.Intn(128))
			}

			b.ResetTimer()

			b.Run("bytes.Buffer", func(b *testing.B) {
				for n := 0; n < b.N; n++ {
					buff := bytes.NewBuffer(nil)

					err := writeByChunksBenchmark(buff, slice, bench.writeChunkSize)
					if err != nil {
						b.Fatalf("error during Write(): %s", err)
					}

					_, err = readByChunksBenchmark(buff, bench.readChunkSize)
					if err != nil {
						b.Fatalf("error during Read(): %s", err)
					}
				}
			})

			b.Run("utils.Buffer", func(b *testing.B) {
				for n := 0; n < b.N; n++ {
					buff := NewBufferWithMaxMemorySize(bench.maxBufferSize)
					defer buff.Reset()

					err := writeByChunksBenchmark(buff, slice, bench.writeChunkSize)
					if err != nil {
						b.Fatalf("error during Write(): %s", err)
					}

					_, err = readByChunksBenchmark(buff, bench.readChunkSize)
					if err != nil {
						b.Fatalf("error during Read(): %s", err)
					}
				}
			})
		})
	}
}

func writeByChunksBenchmark(w io.Writer, source []byte, chunk int) error {
	// Write slice by chunks
	for i := 0; i < len(source); i += chunk {
		bound := i + chunk
		if bound > len(source) {
			bound = len(source)
		}

		_, err := w.Write(source[i:bound])
		if err != nil {
			return err
		}
	}

	return nil
}

func readByChunksBenchmark(r io.Reader, chunk int) ([]byte, error) {
	var res []byte

	data := make([]byte, chunk)
	for {
		n, err := r.Read(data)
		data = data[:n]
		res = append(res, data...)
		data = data[:cap(data)]

		if err != nil {
			if err == io.EOF {
				break
			}

			return nil, err
		}
	}

	return res, nil
}

func TestBuffer_ReaderAt(t *testing.T) {
	tests := []struct {
		bufMemSize   int
		originalData []byte

		offset       int
		readN        int
		receivedData []byte
	}{
		{
			bufMemSize:   3000,
			originalData: []byte("Hello, world!"),
			offset:       0,
			readN:        5,
			receivedData: []byte("Hello"),
		},
		{
			bufMemSize:   3000,
			originalData: []byte("Hello, world!"),
			offset:       2,
			readN:        2,
			receivedData: []byte("ll"),
		},
		{
			bufMemSize:   2,
			originalData: []byte("Hello, world!"),
			offset:       2,
			readN:        2,
			receivedData: []byte("ll"),
		},
		{
			bufMemSize:   2,
			originalData: []byte("Hello, world!"),
			offset:       3,
			readN:        2,
			receivedData: []byte("lo"),
		},
		{ // read half from buf half from file
			bufMemSize:   2,
			originalData: []byte("Hello, world!"),
			offset:       1,
			readN:        3,
			receivedData: []byte("ell"),
		},
		{ // read half from buf half from file
			bufMemSize:   2,
			originalData: []byte("Hello, world!"),
			offset:       0,
			readN:        300,
			receivedData: []byte("Hello, world!"),
		},
		{ // read half from buf half from file
			bufMemSize:   2,
			originalData: []byte("Hello, world!"),
			offset:       4,
			readN:        300,
			receivedData: []byte("o, world!"),
		},
		{
			bufMemSize:   0,
			originalData: []byte("test"),
			offset:       1,
			readN:        300,
			receivedData: []byte("est"),
		},
		{
			bufMemSize:   0,
			originalData: []byte(""),
			offset:       76,
			readN:        0,
			receivedData: []byte(""),
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run("", func(t *testing.T) {
			t.Parallel()

			require := require.New(t)

			b := newBufWithSize(tt.originalData, tt.bufMemSize)

			defer b.Reset()

			data := make([]byte, len(tt.receivedData))
			_, err := b.ReadAt(data, int64(tt.offset))
			if err != nil && !errors.Is(err, io.EOF) {
				t.Fatalf("err: %s", err.Error())
			}
			require.Equal(tt.receivedData, data)
		})
	}
}

func TestReadAt(t *testing.T) {
	require := require.New(t)

	licData, err := os.ReadFile("LICENSE")
	require.NoError(err)

	b := newBufWithSize(licData, 10)

	defer b.Reset()

	receivedData := make([]byte, 15)
	_, err = b.ReadAt(receivedData, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("err: %s", err.Error())
	}
	require.EqualValues("The MIT License", receivedData)

	_, err = b.ReadAt(receivedData, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("err: %s", err.Error())
	}
	require.EqualValues("The MIT License", receivedData)

	_, err = b.ReadAt(receivedData, 2)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("err: %s", err.Error())
	}
	require.EqualValues("e MIT License (", receivedData)

	n, err := b.ReadAt(receivedData, 20)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("err: %s", err.Error())
	}
	require.EqualValues(")\n\nCopyright (c", receivedData)
	require.Equal(len(receivedData), n)
}

func newBufWithSize(buf []byte, size int) *Buffer {
	b := NewBufferWithMaxMemorySize(size)
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

func FuzzReaderAt(f *testing.F) {
	// target, can be only one per test
	// values of data and randOffset will be auto-generated
	f.Fuzz(func(t *testing.T, data []byte, randOffset int64) {
		if randOffset <= 0 || len(data) == 0 {
			return
		}
		newData := make([]byte, len(data))
		buf := newBufWithSize(data, 10)
		defer buf.Reset()
		_, err := buf.ReadAt(newData, int64(randOffset))
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}

		if len(data) > int(randOffset) {
			require.EqualValues(t, string(data[randOffset:]), string(newData[:len(data[randOffset:])]))
		}
	})
}
