package storage

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeS3 is an in-process, path-style S3 service: just the calls the s3
// driver makes (HeadBucket, ListObjectsV2, Put/Get/Head/DeleteObject and
// the multipart upload), with S3's own error shapes. It checks the access
// key and nothing else of the signature.
type fakeS3 struct {
	accessKey string

	mu      sync.Mutex
	buckets map[string]map[string]fakeObject
	uploads map[string]*fakeUpload
	nextID  int
	// Counted for the tests: single PUTs, completed multipart uploads, and
	// the headers each upload request carried.
	singlePuts     int
	multipartDone  int
	partSizes      []int
	requestHeaders []http.Header
}

type fakeObject struct {
	body        []byte
	contentType string
	modified    time.Time
}

type fakeUpload struct {
	bucket, key, contentType string
	parts                    map[int][]byte
}

// newFakeS3 serves a fake with the given buckets over TLS and returns an
// S3Options pointing at it.
func newFakeS3(t *testing.T, buckets ...string) (*fakeS3, S3Options) {
	t.Helper()
	f := &fakeS3{accessKey: "test-access-key", buckets: map[string]map[string]fakeObject{}, uploads: map[string]*fakeUpload{}}
	for _, b := range buckets {
		f.buckets[b] = map[string]fakeObject{}
	}
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return f, S3Options{
		Endpoint: u.Host, Secure: true, Region: "europe-1", Bucket: "tenant",
		AccessKeyID: f.accessKey, SecretAccessKey: "test-secret-key", PathStyle: true,
		Transport: srv.Client().Transport,
	}
}

func (f *fakeS3) objects(bucket string) map[string]fakeObject {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buckets[bucket]
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !strings.Contains(r.Header.Get("Authorization"), "Credential="+f.accessKey+"/") {
		s3Error(w, http.StatusForbidden, "InvalidAccessKeyId", r.Method)
		return
	}
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	objects, ok := f.buckets[bucket]
	if !ok {
		s3Error(w, http.StatusNotFound, "NoSuchBucket", r.Method)
		return
	}
	q := r.URL.Query()

	if key == "" {
		switch {
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && q.Has("location"):
			writeXML(w, struct {
				XMLName xml.Name `xml:"LocationConstraint"`
				Value   string   `xml:",chardata"`
			}{Value: "europe-1"})
		case r.Method == http.MethodGet && q.Get("list-type") == "2":
			f.list(w, objects, q.Get("prefix"))
		default:
			s3Error(w, http.StatusNotImplemented, "NotImplemented", r.Method)
		}
		return
	}

	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.nextID++
		id := fmt.Sprintf("upload-%d", f.nextID)
		f.uploads[id] = &fakeUpload{bucket: bucket, key: key, contentType: r.Header.Get("Content-Type"), parts: map[int][]byte{}}
		f.requestHeaders = append(f.requestHeaders, r.Header.Clone())
		writeXML(w, struct {
			XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
			Bucket   string
			Key      string
			UploadId string //nolint:revive // S3's element name
		}{Bucket: bucket, Key: key, UploadId: id})
	case r.Method == http.MethodPut && q.Has("uploadId"):
		up, ok := f.uploads[q.Get("uploadId")]
		if !ok {
			s3Error(w, http.StatusNotFound, "NoSuchUpload", r.Method)
			return
		}
		n, _ := strconv.Atoi(q.Get("partNumber"))
		body := readBody(r)
		up.parts[n] = body
		f.partSizes = append(f.partSizes, len(body))
		f.requestHeaders = append(f.requestHeaders, r.Header.Clone())
		w.Header().Set("ETag", etag(body))
	case r.Method == http.MethodPost && q.Has("uploadId"):
		up, ok := f.uploads[q.Get("uploadId")]
		if !ok {
			s3Error(w, http.StatusNotFound, "NoSuchUpload", r.Method)
			return
		}
		numbers := make([]int, 0, len(up.parts))
		for n := range up.parts {
			numbers = append(numbers, n)
		}
		slices.Sort(numbers)
		var all []byte
		for _, n := range numbers {
			all = append(all, up.parts[n]...)
		}
		objects[key] = fakeObject{body: all, contentType: up.contentType, modified: time.Now()}
		delete(f.uploads, q.Get("uploadId"))
		f.multipartDone++
		writeXML(w, struct {
			XMLName xml.Name `xml:"CompleteMultipartUploadResult"`
			Bucket  string
			Key     string
			ETag    string
		}{Bucket: bucket, Key: key, ETag: etag(all)})
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		delete(f.uploads, q.Get("uploadId"))
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut:
		body := readBody(r)
		objects[key] = fakeObject{body: body, contentType: r.Header.Get("Content-Type"), modified: time.Now()}
		f.singlePuts++
		f.requestHeaders = append(f.requestHeaders, r.Header.Clone())
		w.Header().Set("ETag", etag(body))
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		obj, ok := objects[key]
		if !ok {
			s3Error(w, http.StatusNotFound, "NoSuchKey", r.Method)
			return
		}
		w.Header().Set("Content-Type", obj.contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.body)))
		w.Header().Set("ETag", etag(obj.body))
		w.Header().Set("Last-Modified", obj.modified.UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(obj.body)
		}
	case r.Method == http.MethodDelete:
		delete(objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		s3Error(w, http.StatusNotImplemented, "NotImplemented", r.Method)
	}
}

func (f *fakeS3) list(w http.ResponseWriter, objects map[string]fakeObject, prefix string) {
	type content struct {
		Key          string
		Size         int64
		ETag         string
		LastModified string
	}
	var out struct {
		XMLName     xml.Name `xml:"ListBucketResult"`
		Prefix      string
		KeyCount    int
		IsTruncated bool
		Contents    []content
	}
	out.Prefix = prefix
	keys := make([]string, 0, len(objects))
	for k := range objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		obj := objects[k]
		out.Contents = append(out.Contents, content{Key: k, Size: int64(len(obj.body)), ETag: etag(obj.body), LastModified: obj.modified.UTC().Format(time.RFC3339)})
	}
	out.KeyCount = len(out.Contents)
	writeXML(w, out)
}

// readBody reads a request body, decoding aws-chunked framing when the
// client used a streaming signature.
func readBody(r *http.Request) []byte {
	raw, _ := io.ReadAll(r.Body)
	if !strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
		return raw
	}
	var out []byte
	br := bufio.NewReader(bytes.NewReader(raw))
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return out
		}
		sizeHex, _, _ := strings.Cut(strings.TrimSpace(line), ";")
		n, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil || n == 0 {
			return out
		}
		chunk := make([]byte, n)
		_, _ = io.ReadFull(br, chunk)
		out = append(out, chunk...)
		_, _ = br.ReadString('\n')
	}
}

func etag(b []byte) string {
	sum := md5.Sum(b)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func writeXML(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(v)
}

// s3Error answers as S3 does: an XML body with the code, except to a HEAD,
// whose response has no body.
func s3Error(w http.ResponseWriter, status int, code, method string) {
	if method == http.MethodHead {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(struct {
		XMLName xml.Name `xml:"Error"`
		Code    string
		Message string
	}{Code: code, Message: code})
}
