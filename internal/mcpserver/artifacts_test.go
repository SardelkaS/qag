package mcpserver

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"
)

func TestArtifactChunksBoundsOwnershipAndAtomicGroups(t *testing.T) {
	s := NewArtifactStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	data := bytes.Repeat([]byte("Русский 9007199254740993.125\n"), 60000)
	group, err := s.PutGroup([]File{{Name: "result.json", MIMEType: "application/json", Data: data}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Read(group[0].ID, 0, 0, true); err == nil {
		t.Fatal("large inline read")
	}
	var restored []byte
	for offset := int64(0); ; {
		part, e := s.Chunk(group[0].ID, offset, 0)
		if e != nil {
			t.Fatal(e)
		}
		b, e := base64.StdEncoding.DecodeString(part.DataBase64)
		if e != nil {
			t.Fatal(e)
		}
		restored = append(restored, b...)
		offset += int64(part.Length)
		if part.EOF {
			break
		}
	}
	if !bytes.Equal(data, restored) {
		t.Fatal("chunk precision/UTF-8 lost")
	}
	for _, offset := range []int64{-1, int64(len(data)) + 1} {
		if _, err = s.Chunk(group[0].ID, offset, 1); err == nil {
			t.Fatal("invalid offset")
		}
	}
	if _, err = s.Chunk(group[0].ID, 0, ChunkBytes+1); err == nil {
		t.Fatal("oversized chunk")
	}
	if _, err = s.Chunk("../../private", 0, 1); err == nil {
		t.Fatal("path accepted")
	}
	s.maxCount = 2
	s.maxBytes = 12
	s.maxFile = 8
	old := s.List()
	if _, err = s.PutGroup([]File{{MIMEType: "text/plain", Data: make([]byte, 9)}}); err == nil {
		t.Fatal("oversized file accepted")
	}
	if len(s.List()) != len(old) {
		t.Fatal("failed group evicted data")
	}
	// A new store makes the small memory budget independent of the large fixture.
	s = NewArtifactStore()
	s.now = func() time.Time { return now }
	s.maxCount = 2
	s.maxBytes = 12
	s.maxFile = 8
	group, err = s.PutGroup([]File{{MIMEType: "text/plain", Data: []byte("a")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutGroup([]File{{MIMEType: "text/plain", Data: []byte("new")}, {MIMEType: "text/plain", Data: []byte("next")}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Chunk(group[0].ID, 0, 1); err == nil {
		t.Fatal("old artifact not evicted")
	}
	now = now.Add(11 * time.Minute)
	if len(s.List()) != 0 {
		t.Fatal("expiry did not clear store")
	}
}
