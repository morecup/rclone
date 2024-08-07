package readers

import "io"

type MultiReadCloser struct {
	io.Reader
	Closers []io.Closer
}

func (m *MultiReadCloser) Close() (err error) {
	for _, closer := range m.Closers {
		if e := closer.Close(); e != nil {
			err = e
		}
	}
	return
}
func NewMutiReadCloser(reader []io.Reader, closer []io.Closer) *MultiReadCloser {
	return &MultiReadCloser{
		Reader:  io.MultiReader(reader...),
		Closers: closer,
	}
}
