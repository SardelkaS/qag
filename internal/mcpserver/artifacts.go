package mcpserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"sort"
	"sync"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/problem"
)

const (
	InlineBytes      = 1 << 20
	ChunkBytes       = 256 << 10
	MaxArtifactBytes = 64 << 20
)

type Artifact struct {
	ID        string    `json:"id"`
	URI       string    `json:"uri"`
	Name      string    `json:"name"`
	MIMEType  string    `json:"mimeType"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type File struct {
	Name, MIMEType string
	Data           []byte
}
type artifactEntry struct {
	Artifact
	data  []byte
	order uint64
}
type ArtifactStore struct {
	mu                                 sync.Mutex
	entries                            map[string]artifactEntry
	bytes, maxBytes, maxCount, maxFile int
	order                              uint64
	ttl                                time.Duration
	now                                func() time.Time
}

func NewArtifactStore() *ArtifactStore {
	return &ArtifactStore{entries: map[string]artifactEntry{}, maxBytes: 256 << 20, maxCount: 32, maxFile: MaxArtifactBytes, ttl: 10 * time.Minute, now: time.Now}
}
func (s *ArtifactStore) remove(id string) { s.bytes -= len(s.entries[id].data); delete(s.entries, id) }
func (s *ArtifactStore) expire() {
	for id, e := range s.entries {
		if !s.now().Before(e.ExpiresAt) {
			s.remove(id)
		}
	}
}
func (s *ArtifactStore) PutGroup(files []File) ([]Artifact, error) {
	if len(files) == 0 || len(files) > s.maxCount {
		return nil, problem.Invalid("artifacts", "Недопустимый размер группы файлов")
	}
	total := 0
	for _, f := range files {
		if len(f.Data) > s.maxFile || f.MIMEType == "" {
			return nil, problem.Invalid("artifacts", "Файл превышает допустимый предел или не имеет MIME-типа")
		}
		total += len(f.Data)
		if total > s.maxBytes {
			return nil, problem.Invalid("artifacts", "Группа превышает предел памяти")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	for len(s.entries)+len(files) > s.maxCount || s.bytes+total > s.maxBytes {
		var oldest string
		var order uint64
		for id, e := range s.entries {
			if oldest == "" || e.order < order {
				oldest = id
				order = e.order
			}
		}
		s.remove(oldest)
	}
	result := make([]Artifact, 0, len(files))
	for _, f := range files {
		id := catalog.NewID()
		hash := sha256.Sum256(f.Data)
		a := Artifact{ID: id, URI: "qag://artifacts/" + id, Name: f.Name, MIMEType: f.MIMEType, Size: int64(len(f.Data)), SHA256: hex.EncodeToString(hash[:]), ExpiresAt: s.now().Add(s.ttl)}
		s.order++
		s.entries[id] = artifactEntry{Artifact: a, data: append([]byte(nil), f.Data...), order: s.order}
		s.bytes += len(f.Data)
		result = append(result, a)
	}
	return result, nil
}
func (s *ArtifactStore) List() []Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	out := make([]Artifact, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e.Artifact)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *ArtifactStore) Read(id string, offset int64, length int, whole bool) (Artifact, []byte, error) {
	if !catalog.ValidID(id) || offset < 0 || length < 0 || (!whole && length > ChunkBytes) {
		return Artifact{}, nil, problem.Invalid("artifact", "Некорректный ID, смещение или размер части")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	e, ok := s.entries[id]
	if !ok {
		return Artifact{}, nil, &problem.Error{Code: "artifact_expired", Message: "Артефакт недоступен или истёк; подготовьте его заново"}
	}
	if whole {
		if len(e.data) > InlineBytes {
			return e.Artifact, nil, &problem.Error{Code: "artifact_too_large", Message: "Используйте qag_artifact_read для чтения частей"}
		}
		length = len(e.data)
	} else if length == 0 {
		length = ChunkBytes
	}
	if offset > int64(len(e.data)) {
		return Artifact{}, nil, problem.Invalid("offset", "Смещение выходит за границу файла")
	}
	end := min(int64(len(e.data)), offset+int64(length))
	return e.Artifact, append([]byte{}, e.data[offset:end]...), nil
}

type ArtifactChunk struct {
	Artifact   Artifact `json:"artifact"`
	Offset     int64    `json:"offset"`
	Length     int      `json:"length"`
	EOF        bool     `json:"eof"`
	DataBase64 string   `json:"dataBase64"`
}

func (s *ArtifactStore) Chunk(id string, offset int64, length int) (ArtifactChunk, error) {
	a, b, e := s.Read(id, offset, length, false)
	if e != nil {
		return ArtifactChunk{}, e
	}
	return ArtifactChunk{Artifact: a, Offset: offset, Length: len(b), EOF: offset+int64(len(b)) == a.Size, DataBase64: base64.StdEncoding.EncodeToString(b)}, nil
}
