package diagnostics

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type files struct {
	options Options
	file    *os.File
	size    int64
}

func openFiles(options Options) (*files, error) {
	if options.MaxBytes < 65536 || options.MaxFiles < 1 || options.MaxFiles > 100 || options.Directory == "" {
		return nil, errors.New("invalid diagnostics file limits")
	}

	if err := os.MkdirAll(options.Directory, 0700); err != nil {
		return nil, errors.New("could not create diagnostics directory")
	}
	f := &files{
		options: options,
	}
	if err := f.open(); err != nil {
		return nil, err
	}

	return f, nil
}

func (f *files) path(index int) string {
	name := "beta.jsonl"
	if index > 0 {
		name = fmt.Sprintf("beta.%d.jsonl", index)
	}

	return filepath.Join(f.options.Directory, name)
}

func (f *files) open() error {
	file, err := os.OpenFile(f.path(0), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return errors.New("could not open diagnostics file")
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()

		return err
	}
	// Discard an incomplete final record left by a crash or interrupted write.
	size := info.Size()
	if size > 0 {
		tail := make([]byte, min(size, 1<<20))
		if _, err := file.ReadAt(tail, size-int64(len(tail))); err != nil {
			file.Close()

			return err
		}
		complete := int64(0)
		if last := bytes.LastIndexByte(tail, '\n'); last >= 0 {
			complete = size - int64(len(tail)) + int64(last) + 1
		}

		if complete != size {
			if err := file.Truncate(complete); err != nil {
				file.Close()

				return err
			}
			size = complete
		}
	}
	if _, err := file.Seek(size, io.SeekStart); err != nil {
		file.Close()

		return err
	}
	f.file = file
	f.size = size

	return nil
}

func (f *files) rotate() error {
	if f.file != nil {
		if err := f.file.Close(); err != nil {
			return err
		}
		f.file = nil
	}
	if err := os.Remove(f.path(f.options.MaxFiles - 1)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := f.options.MaxFiles - 2; i >= 0; i-- {
		if err := os.Rename(f.path(i), f.path(i+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	return f.open()
}

func (f *files) Write(data []byte) (int, error) {
	if int64(len(data)) > f.options.MaxBytes {
		return 0, errors.New("diagnostics record exceeds file limit")
	}

	if f.size+int64(len(data)) > f.options.MaxBytes {
		if err := f.rotate(); err != nil {
			return 0, err
		}
	}

	if f.file == nil {
		if err := f.open(); err != nil {
			return 0, err
		}
	}
	n, err := f.file.Write(data)
	if n != len(data) && err == nil {
		err = io.ErrShortWrite
	}

	if err != nil {
		// Roll back partial JSON so the next successful record remains readable.
		repairErr := f.file.Truncate(f.size)
		if repairErr == nil {
			_, repairErr = f.file.Seek(f.size, io.SeekStart)
		}
		if repairErr != nil {
			f.file.Close()
			f.file = nil
		}

		return n, errors.Join(err, repairErr)
	}
	f.size += int64(n)

	return n, err
}

func (f *files) Close() error {
	if f.file == nil {
		return nil
	}

	return errors.Join(f.file.Sync(), f.file.Close())
}
