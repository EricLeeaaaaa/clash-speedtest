package main

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
)

var zeroBytes = make([]byte, 1024*1024)

type ZeroReader struct {
	remainBytes  int64
	writtenBytes int64
}

func NewZeroReader(size int) *ZeroReader {
	return &ZeroReader{
		remainBytes:  int64(size),
		writtenBytes: 0,
	}
}

func (r *ZeroReader) Read(p []byte) (n int, err error) {
	if r.remainBytes <= 0 {
		return 0, io.EOF
	}
	toRead := int64(len(p))
	if toRead > r.remainBytes {
		toRead = r.remainBytes
	}
	bytesWritten := int64(0)
	for bytesWritten < toRead {
		chunk := toRead - bytesWritten
		if chunk > int64(len(zeroBytes)) {
			chunk = int64(len(zeroBytes))
		}
		copy(p[bytesWritten:], zeroBytes[:chunk])
		bytesWritten += chunk
	}
	r.remainBytes -= bytesWritten
	r.writtenBytes += bytesWritten
	return int(bytesWritten), nil
}

func (r *ZeroReader) WrittenBytes() int64 {
	return r.writtenBytes
}

func (r *ZeroReader) RemainBytes() int64 {
	return r.remainBytes
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<h1>SpeedTest Server</h1>`))
	})

	http.HandleFunc("/__down", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		byteSize, err := strconv.Atoi(r.URL.Query().Get("bytes"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(err.Error()))
			return
		}

		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=speedtest-%d.bin", byteSize))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)

		reader := NewZeroReader(byteSize)
		io.Copy(w, reader)
	})

	http.HandleFunc("/__up", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		io.Copy(io.Discard, r.Body)

		w.WriteHeader(http.StatusOK)
	})

	http.ListenAndServe(":8080", nil)
}
