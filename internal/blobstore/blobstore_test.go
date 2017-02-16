// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package blobstore_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/blobstore"

import (
	"fmt"
	"io"
	"io/ioutil"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/juju/charmstore.v5-unstable/internal/blobstore"
)

func TestPackage(t *testing.T) {
	jujutesting.MgoTestPackage(t, nil)
}

type BlobStoreSuite struct {
	jujutesting.IsolatedMgoSuite
	store *blobstore.Store
}

var _ = gc.Suite(&BlobStoreSuite{})

func (s *BlobStoreSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.store = blobstore.New(s.Session.DB("db"), "blobstore")
}

func (s *BlobStoreSuite) TestPutTwice(c *gc.C) {
	content := "some data"
	err := s.store.Put(strings.NewReader(content), "x", int64(len(content)), hashOf(content))
	c.Assert(err, gc.IsNil)

	content = "some different data"
	err = s.store.Put(strings.NewReader(content), "x", int64(len(content)), hashOf(content))
	c.Assert(err, gc.IsNil)

	rc, length, err := s.store.Open("x", nil)
	c.Assert(err, gc.IsNil)
	defer rc.Close()
	c.Assert(length, gc.Equals, int64(len(content)))

	data, err := ioutil.ReadAll(rc)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, content)
}

func (s *BlobStoreSuite) TestPut(c *gc.C) {
	content := "some data"
	err := s.store.Put(strings.NewReader(content), "x", int64(len(content)), hashOf(content))
	c.Assert(err, gc.IsNil)

	rc, length, err := s.store.Open("x", nil)
	c.Assert(err, gc.IsNil)
	defer rc.Close()
	c.Assert(length, gc.Equals, int64(len(content)))

	data, err := ioutil.ReadAll(rc)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, content)

	err = s.store.Put(strings.NewReader(content), "x", int64(len(content)), hashOf(content))
	c.Assert(err, gc.IsNil)
}

func (s *BlobStoreSuite) TestPutInvalidHash(c *gc.C) {
	content := "some data"
	err := s.store.Put(strings.NewReader(content), "x", int64(len(content)), hashOf("wrong"))
	c.Assert(err, gc.ErrorMatches, "hash mismatch")
}

func (s *BlobStoreSuite) TestRemove(c *gc.C) {
	content := "some data"
	err := s.store.Put(strings.NewReader(content), "x", int64(len(content)), hashOf(content))
	c.Assert(err, gc.IsNil)

	rc, length, err := s.store.Open("x", nil)
	c.Assert(err, gc.IsNil)
	defer rc.Close()
	c.Assert(length, gc.Equals, int64(len(content)))
	data, err := ioutil.ReadAll(rc)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, content)

	err = s.store.Remove("x", nil)
	c.Assert(err, gc.IsNil)

	rc, length, err = s.store.Open("x", nil)
	c.Assert(err, gc.ErrorMatches, `resource at path "[^"]+" not found`)
}

func (s *BlobStoreSuite) TestNewParts(c *gc.C) {
	expires := time.Now().Add(time.Minute).UTC().Truncate(time.Millisecond)
	id, err := s.store.NewUpload(expires)
	c.Assert(err, gc.Equals, nil)
	c.Assert(id, gc.Not(gc.Equals), "")

	// Verify that the new record looks like we expect.
	var udoc blobstore.UploadDoc
	err = s.Session.DB("db").C("blobstore.upload").FindId(id).One(&udoc)
	c.Assert(err, gc.Equals, nil)
	c.Assert(udoc, jc.DeepEquals, blobstore.UploadDoc{
		Id:      id,
		Expires: expires,
	})
}

func (s *BlobStoreSuite) TestPutPartNegativePart(c *gc.C) {
	id := s.newUpload(c)

	err := s.store.PutPart(id, -1, nil, 0, "")
	c.Assert(err, gc.ErrorMatches, "negative part number")
}

func (s *BlobStoreSuite) TestPutPartNumberTooBig(c *gc.C) {
	s.PatchValue(blobstore.MaxParts, 100)

	id := s.newUpload(c)
	err := s.store.PutPart(id, 100, nil, 0, "")
	c.Assert(err, gc.ErrorMatches, `part number 100 too big \(maximum 99\)`)
}

func (s *BlobStoreSuite) TestPutPartSizeNonPositive(c *gc.C) {
	id := s.newUpload(c)
	err := s.store.PutPart(id, 0, strings.NewReader(""), 0, hashOf(""))
	c.Assert(err, gc.ErrorMatches, `non-positive part 0 size 0`)
}

func (s *BlobStoreSuite) TestPutPartSizeTooBig(c *gc.C) {
	s.PatchValue(blobstore.MaxPartSize, int64(5))

	id := s.newUpload(c)
	err := s.store.PutPart(id, 0, strings.NewReader(""), 20, hashOf(""))
	c.Assert(err, gc.ErrorMatches, `part 0 too big \(maximum 5\)`)
}

func (s *BlobStoreSuite) TestPutPartSingle(c *gc.C) {
	id := s.newUpload(c)

	content := "123456789 12345"
	err := s.store.PutPart(id, 0, strings.NewReader(content), int64(len(content)), hashOf(content))
	c.Assert(err, gc.Equals, nil)

	r, size, err := s.store.Open(id+"/0", nil)
	c.Assert(err, gc.Equals, nil)
	c.Assert(size, gc.Equals, int64(len(content)))
	c.Assert(hashOfReader(c, r), gc.Equals, hashOf(content))
}

func (s *BlobStoreSuite) TestPutPartAgain(c *gc.C) {
	id := s.newUpload(c)

	content := "123456789 12345"

	// Perform a Put with mismatching content. This should leave the part in progress
	// but not completed.
	err := s.store.PutPart(id, 0, strings.NewReader("something different"), int64(len(content)), hashOf(content))
	c.Assert(err, gc.ErrorMatches, `cannot upload part ".+": hash mismatch`)

	// Try again with the correct content this time.
	err = s.store.PutPart(id, 0, strings.NewReader(content), int64(len(content)), hashOf(content))
	c.Assert(err, gc.Equals, nil)

	r, size, err := s.store.Open(id+"/0", nil)
	c.Assert(err, gc.Equals, nil)
	c.Assert(size, gc.Equals, int64(len(content)))
	c.Assert(hashOfReader(c, r), gc.Equals, hashOf(content))
}

func (s *BlobStoreSuite) TestPutPartAgainWithDifferentHash(c *gc.C) {
	id := s.newUpload(c)

	content := "123456789 12345"
	err := s.store.PutPart(id, 0, strings.NewReader(content), int64(len(content)), hashOf(content))
	c.Assert(err, gc.Equals, nil)

	content1 := "abcdefghijklmnopqrstuvwxyz"
	err = s.store.PutPart(id, 0, strings.NewReader(content1), int64(len(content1)), hashOf(content1))
	c.Assert(err, gc.ErrorMatches, `hash mismatch for already uploaded part`)
}

func (s *BlobStoreSuite) TestPutPartAgainWithSameHash(c *gc.C) {
	id := s.newUpload(c)

	content := "123456789 12345"
	err := s.store.PutPart(id, 0, strings.NewReader(content), int64(len(content)), hashOf(content))
	c.Assert(err, gc.Equals, nil)

	err = s.store.PutPart(id, 0, strings.NewReader(content), int64(len(content)), hashOf(content))
	c.Assert(err, gc.Equals, nil)
}

func (s *BlobStoreSuite) TestPutPartOutOfOrder(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(10))
	id := s.newUpload(c)

	content1 := "123456789 123456789 "
	err := s.store.PutPart(id, 1, strings.NewReader(content1), int64(len(content1)), hashOf(content1))
	c.Assert(err, gc.Equals, nil)

	content0 := "abcdefghijklmnopqrstuvwxyz"
	err = s.store.PutPart(id, 0, strings.NewReader(content0), int64(len(content0)), hashOf(content0))
	c.Assert(err, gc.Equals, nil)

	r, size, err := s.store.Open(id+"/0", nil)
	c.Assert(err, gc.Equals, nil)
	c.Assert(size, gc.Equals, int64(len(content0)))
	c.Assert(hashOfReader(c, r), gc.Equals, hashOf(content0))

	r, size, err = s.store.Open(id+"/1", nil)
	c.Assert(err, gc.Equals, nil)
	c.Assert(size, gc.Equals, int64(len(content1)))
	c.Assert(hashOfReader(c, r), gc.Equals, hashOf(content1))
}

func (s *BlobStoreSuite) TestPutPartTooSmall(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(100))
	id := s.newUpload(c)

	content0 := "abcdefghijklmnopqrstuvwxyz"
	err := s.store.PutPart(id, 0, strings.NewReader(content0), int64(len(content0)), hashOf(content0))
	c.Assert(err, gc.Equals, nil)

	content1 := "123456789 123456789 "
	err = s.store.PutPart(id, 1, strings.NewReader(content1), int64(len(content1)), hashOf(content1))
	c.Assert(err, gc.ErrorMatches, `part 0 was too small \(need at least 100 bytes, got 26\)`)
}

func (s *BlobStoreSuite) TestPutPartTooSmallOutOfOrder(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(100))
	id := s.newUpload(c)

	content1 := "abcdefghijklmnopqrstuvwxyz"
	err := s.store.PutPart(id, 1, strings.NewReader(content1), int64(len(content1)), hashOf(content1))
	c.Assert(err, gc.Equals, nil)

	content0 := "123456789 123456789 "
	err = s.store.PutPart(id, 0, strings.NewReader(content0), int64(len(content0)), hashOf(content0))
	c.Assert(err, gc.ErrorMatches, `part too small \(need at least 100 bytes, got 20\)`)
}

func (s *BlobStoreSuite) TestPutPartSmallAtEnd(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(10))
	id := s.newUpload(c)

	content0 := "1234"
	err := s.store.PutPart(id, 0, strings.NewReader(content0), int64(len(content0)), hashOf(content0))
	c.Assert(err, gc.Equals, nil)

	content1 := "abc"
	err = s.store.PutPart(id, 1, strings.NewReader(content1), int64(len(content1)), hashOf(content1))
	c.Assert(err, gc.ErrorMatches, `part 0 was too small \(need at least 10 bytes, got 4\)`)
}

func (s *BlobStoreSuite) TestPutPartConcurrent(c *gc.C) {
	id := s.newUpload(c)
	var hash [3]string
	const size = 5 * 1024 * 1024
	for i := range hash {
		hash[i] = hashOfReader(c, newDataSource(int64(i+1), size))
	}
	var wg sync.WaitGroup
	for i := range hash {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Make a copy of the session so we get independent
			// mongo sockets and more concurrency.
			db := s.Session.Copy().DB("db")
			defer db.Session.Close()
			store := blobstore.New(db, "blobstore")
			err := store.PutPart(id, i, newDataSource(int64(i+1), size), size, hash[i])
			c.Check(err, gc.IsNil)
		}()
	}
	wg.Wait()
	for i := range hash {
		r, size, err := s.store.Open(fmt.Sprintf("%s/%d", id, i), nil)
		c.Assert(err, gc.Equals, nil)
		c.Assert(size, gc.Equals, size)
		c.Assert(hashOfReader(c, r), gc.Equals, hash[i])
	}
}

func (s *BlobStoreSuite) TestPutPartNotFound(c *gc.C) {
	err := s.store.PutPart("unknownblob", 0, strings.NewReader("x"), 1, hashOf(""))
	c.Assert(err, gc.ErrorMatches, `upload id "unknownblob" not found`)
	c.Assert(errgo.Cause(err), gc.Equals, blobstore.ErrNotFound)
}

func (s *BlobStoreSuite) TestFinishUploadMismatchedPartCount(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(10))
	id := s.newUpload(c)

	content0 := "123456789 123456789 "
	err := s.store.PutPart(id, 0, strings.NewReader(content0), int64(len(content0)), hashOf(content0))
	c.Assert(err, gc.Equals, nil)

	content1 := "abcdefghijklmnopqrstuvwxyz"
	err = s.store.PutPart(id, 1, strings.NewReader(content1), int64(len(content1)), hashOf(content1))
	c.Assert(err, gc.Equals, nil)

	idx, hash, err := s.store.FinishUpload(id, []blobstore.Part{{
		Hash: hashOf(content0),
	}})
	c.Assert(err, gc.ErrorMatches, `part count mismatch \(got 1 but 2 uploaded\)`)
	c.Assert(idx, gc.IsNil)
	c.Assert(hash, gc.Equals, "")
}

func (s *BlobStoreSuite) TestFinishUploadMismatchedPartHash(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(10))
	id := s.newUpload(c)

	content0 := "123456789 123456789 "
	err := s.store.PutPart(id, 0, strings.NewReader(content0), int64(len(content0)), hashOf(content0))
	c.Assert(err, gc.Equals, nil)

	content1 := "abcdefghijklmnopqrstuvwxyz"
	err = s.store.PutPart(id, 1, strings.NewReader(content1), int64(len(content1)), hashOf(content1))
	c.Assert(err, gc.Equals, nil)

	idx, hash, err := s.store.FinishUpload(id, []blobstore.Part{{
		Hash: hashOf(content0),
	}, {
		Hash: "badhash",
	}})
	c.Assert(err, gc.ErrorMatches, `hash mismatch on part 1 \(got "badhash" want ".+"\)`)
	c.Assert(idx, gc.IsNil)
	c.Assert(hash, gc.Equals, "")
}

func (s *BlobStoreSuite) TestFinishUploadPartNotUploaded(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(10))
	id := s.newUpload(c)

	content1 := "123456789 123456789 "
	err := s.store.PutPart(id, 1, strings.NewReader(content1), int64(len(content1)), hashOf(content1))
	c.Assert(err, gc.Equals, nil)

	idx, hash, err := s.store.FinishUpload(id, []blobstore.Part{{
		Hash: hashOf(content1),
	}, {
		Hash: hashOf(content1),
	}})
	c.Assert(err, gc.ErrorMatches, `part 0 not uploaded yet`)
	c.Assert(idx, gc.IsNil)
	c.Assert(hash, gc.Equals, "")
}

func (s *BlobStoreSuite) TestFinishUploadPartIncomplete(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(10))
	id := s.newUpload(c)

	content0 := "123456789 123456789 "
	err := s.store.PutPart(id, 0, strings.NewReader(""), int64(len(content0)), hashOf(content0))
	c.Assert(err, gc.ErrorMatches, `cannot upload part ".+/0": hash mismatch`)

	idx, hash, err := s.store.FinishUpload(id, []blobstore.Part{{
		Hash: hashOf(content0),
	}})
	c.Assert(err, gc.ErrorMatches, `part 0 not uploaded yet`)
	c.Assert(idx, gc.IsNil)
	c.Assert(hash, gc.Equals, "")
}

func (s *BlobStoreSuite) TestFinishUploadCheckSizes(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(50))
	id := s.newUpload(c)
	content := "123456789 123456789 "
	// Upload two small parts concurrently.
	done := make(chan error)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			err := s.store.PutPart(id, i, strings.NewReader(content), int64(len(content)), hashOf(content))
			done <- err
		}()
	}
	allOK := true
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			c.Assert(err, gc.ErrorMatches, ".*too small.*")
			allOK = allOK && err == nil
		}
	}
	if !allOK {
		// Although it's likely that both parts will succeed
		// because they both fetch the upload doc at the same
		// time, there's a possibility that one goroutine will
		// fetch and initialize its update doc before the other
		// one retrieves it, so we skip the test in that case
		c.Skip("concurrent uploads were not very concurrent, so test skipped")
	}
	idx, hash, err := s.store.FinishUpload(id, []blobstore.Part{{
		Hash: hashOf(content),
	}, {
		Hash: hashOf(content),
	}})
	c.Assert(err, gc.ErrorMatches, `part 0 was too small \(need at least 50 bytes, got 20\)`)
	c.Assert(idx, gc.IsNil)
	c.Assert(hash, gc.Equals, "")
}

func (s *BlobStoreSuite) TestFinishUploadSuccess(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(10))
	id := s.newUpload(c)

	content0 := "123456789 123456789 "
	err := s.store.PutPart(id, 0, strings.NewReader(content0), int64(len(content0)), hashOf(content0))
	c.Assert(err, gc.Equals, nil)

	content1 := "abcdefghijklmnopqrstuvwxyz"
	err = s.store.PutPart(id, 1, strings.NewReader(content1), int64(len(content1)), hashOf(content1))
	c.Assert(err, gc.Equals, nil)

	idx, hash, err := s.store.FinishUpload(id, []blobstore.Part{{
		Hash: hashOf(content0),
	}, {
		Hash: hashOf(content1),
	}})
	c.Assert(err, gc.Equals, nil)
	c.Assert(hash, gc.Equals, hashOf(content0+content1))
	c.Assert(idx, jc.DeepEquals, &blobstore.MultipartIndex{
		Sizes: []uint32{
			uint32(len(content0)),
			uint32(len(content1)),
		},
	})
}

func (s *BlobStoreSuite) TestFinishUploadSuccessOnePart(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(10))
	id := s.newUpload(c)

	content0 := "123456789 123456789 "
	err := s.store.PutPart(id, 0, strings.NewReader(content0), int64(len(content0)), hashOf(content0))
	c.Assert(err, gc.Equals, nil)

	idx, hash, err := s.store.FinishUpload(id, []blobstore.Part{{
		Hash: hashOf(content0),
	}})
	c.Assert(err, gc.Equals, nil)
	c.Assert(hash, gc.Equals, hashOf(content0))
	c.Assert(idx, jc.DeepEquals, &blobstore.MultipartIndex{
		Sizes: []uint32{
			uint32(len(content0)),
		},
	})
}

func (s *BlobStoreSuite) TestFinishUploadNotFound(c *gc.C) {
	_, _, err := s.store.FinishUpload("not-an-id", nil)
	c.Assert(err, gc.ErrorMatches, `upload id "not-an-id" not found`)
	c.Assert(errgo.Cause(err), gc.Equals, blobstore.ErrNotFound)
}

func (s *BlobStoreSuite) TestFinishUploadAgain(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(10))
	id := s.newUpload(c)

	content0 := "123456789 123456789 "
	err := s.store.PutPart(id, 0, strings.NewReader(content0), int64(len(content0)), hashOf(content0))
	c.Assert(err, gc.Equals, nil)

	idx, hash, err := s.store.FinishUpload(id, []blobstore.Part{{
		Hash: hashOf(content0),
	}})
	c.Assert(err, gc.Equals, nil)
	c.Assert(hash, gc.Equals, hashOf(content0))
	c.Assert(idx, jc.DeepEquals, &blobstore.MultipartIndex{
		Sizes: []uint32{
			uint32(len(content0)),
		},
	})

	// We should get exactly the same thing if we call
	// FinishUpload again.
	idx, hash, err = s.store.FinishUpload(id, []blobstore.Part{{
		Hash: hashOf(content0),
	}})
	c.Assert(err, gc.Equals, nil)
	c.Assert(hash, gc.Equals, hashOf(content0))
	c.Assert(idx, jc.DeepEquals, &blobstore.MultipartIndex{
		Sizes: []uint32{
			uint32(len(content0)),
		},
	})
}

func (s *BlobStoreSuite) TestFinishUploadRemovedWhenCalculatingHash(c *gc.C) {
	s.PatchValue(blobstore.MinPartSize, int64(10))
	id := s.newUpload(c)

	// We need at least two parts so that FinishUpload
	// actually needs to stream the parts again, so
	// upload a small first part and then a large second
	// part that's big enough that there's a strong probability
	// that we'll be able to remove the upload entry before
	// FinishUpload has finished calculating the hash.
	content0 := "123456789 123456789 "
	err := s.store.PutPart(id, 0, strings.NewReader(content0), int64(len(content0)), hashOf(content0))
	c.Assert(err, gc.Equals, nil)

	const size1 = 2 * 1024 * 1024
	hash1 := hashOfReader(c, newDataSource(1, size1))
	err = s.store.PutPart(id, 1, newDataSource(1, size1), int64(size1), hash1)
	c.Assert(err, gc.Equals, nil)

	done := make(chan error)
	go func() {
		_, _, err := s.store.FinishUpload(id, []blobstore.Part{{
			Hash: hashOf(content0),
		}, {
			Hash: hash1,
		}})
		done <- err
	}()
	// TODO use DeleteUpload instead of going directly to the collection.
	time.Sleep(20 * time.Millisecond)
	err = s.Session.DB("db").C("blobstore.upload").RemoveId(id)
	c.Assert(err, gc.Equals, nil)

	err = <-done
	if err == nil {
		// We didn't delete it fast enough, so skip the test.
		c.Skip("FinishUpload finished before we could interfere with it")
	}
	if errgo.Cause(err) == blobstore.ErrNotFound {
		c.Skip("FinishUpload started too late, after we removed its doc")
	}
	c.Assert(err, gc.ErrorMatches, `upload expired or removed`)
}

// newUpload returns the id of a new upload instance.
func (s *BlobStoreSuite) newUpload(c *gc.C) string {
	expires := time.Now().Add(time.Minute).UTC()
	id, err := s.store.NewUpload(expires)
	c.Assert(err, gc.Equals, nil)
	return id
}

func hashOfReader(c *gc.C, r io.Reader) string {
	h := blobstore.NewHash()
	_, err := io.Copy(h, r)
	c.Assert(err, gc.IsNil)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func hashOf(s string) string {
	h := blobstore.NewHash()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum(nil))
}

type dataSource struct {
	buf      []byte
	bufIndex int
	remain   int64
}

// newDataSource returns a stream of size bytes holding
// a repeated number.
func newDataSource(fillWith int64, size int64) io.Reader {
	src := &dataSource{
		remain: size,
	}
	for len(src.buf) < 8*1024 {
		src.buf = strconv.AppendInt(src.buf, fillWith, 10)
		src.buf = append(src.buf, ' ')
	}
	return src
}

func (s *dataSource) Read(buf []byte) (int, error) {
	if int64(len(buf)) > s.remain {
		buf = buf[:int(s.remain)]
	}
	total := len(buf)
	if total == 0 {
		return 0, io.EOF
	}

	for len(buf) > 0 {
		if s.bufIndex == len(s.buf) {
			s.bufIndex = 0
		}
		nb := copy(buf, s.buf[s.bufIndex:])
		s.bufIndex += nb
		buf = buf[nb:]
		s.remain -= int64(nb)
	}
	return total, nil
}
