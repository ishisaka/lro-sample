// AIP-151 (Long-running operations) に準拠した最小サンプル。
// 「書籍の再インデックス処理」をユースケースとして、
//   - 開始 RPC (Operation を返す)
//   - GetOperation (ポーリング)
//   - CancelOperation
//
// を実装しています。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// -----------------------------------------------------------------------------
// Operation リソース (google.longrunning.Operation 相当)
// -----------------------------------------------------------------------------

type Operation struct {
	Name     string          `json:"name"`               // "operations/{id}"
	Metadata json.RawMessage `json:"metadata,omitempty"` // metadata_type
	Done     bool            `json:"done"`
	Error    *OperationError `json:"error,omitempty"`    // 失敗時のみ
	Response json.RawMessage `json:"response,omitempty"` // 成功時のみ (response_type)
}

// google.rpc.Status の簡易版
type OperationError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// -----------------------------------------------------------------------------
// ユースケース固有の型
//   - ReindexMetadata: 進捗などをクライアントに返すための型 (metadata_type)
//   - ReindexResponse: 最終結果 (response_type)
// -----------------------------------------------------------------------------

type ReindexMetadata struct {
	Progress   int       `json:"progress"` // 0-100
	StartTime  time.Time `json:"startTime"`
	UpdateTime time.Time `json:"updateTime"`
}

type ReindexResponse struct {
	Publisher    string `json:"publisher"`
	BooksIndexed int    `json:"booksIndexed"`
}

// -----------------------------------------------------------------------------
// インメモリ Operation ストア
// 本番では Redis や RDB など永続層に格納します
// -----------------------------------------------------------------------------

type OperationStore struct {
	mu      sync.RWMutex
	ops     map[string]*Operation
	cancels map[string]context.CancelFunc
}

func NewOperationStore() *OperationStore {
	return &OperationStore{
		ops:     make(map[string]*Operation),
		cancels: make(map[string]context.CancelFunc),
	}
}

func (s *OperationStore) Put(op *Operation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops[op.Name] = op
}

func (s *OperationStore) Get(name string) (*Operation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	op, ok := s.ops[name]
	return op, ok
}

func (s *OperationStore) UpdateMetadata(name string, meta any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.ops[name]
	if !ok {
		return
	}
	b, _ := json.Marshal(meta)
	op.Metadata = b
}

func (s *OperationStore) Complete(name string, response any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.ops[name]
	if !ok {
		return
	}
	b, _ := json.Marshal(response)
	op.Response = b
	op.Done = true
	delete(s.cancels, name) // 完了済みはキャンセル不要
}

func (s *OperationStore) Fail(name string, code int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.ops[name]
	if !ok {
		return
	}
	op.Error = &OperationError{Code: code, Message: message}
	op.Done = true
	delete(s.cancels, name)
}

func (s *OperationStore) RegisterCancel(name string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels[name] = cancel
}

func (s *OperationStore) Cancel(name string) bool {
	s.mu.Lock()
	cancel, ok := s.cancels[name]
	s.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// -----------------------------------------------------------------------------
// HTTP ハンドラ
// -----------------------------------------------------------------------------

type Server struct {
	store *OperationStore
}

// POST /v1/publishers/{publisher}/books:reindex
// 長時間処理を開始して Operation を返します (202 Accepted)
func (s *Server) ReindexBooks(w http.ResponseWriter, r *http.Request) {
	publisher := chi.URLParam(r, "publisher")

	opName := "operations/" + newID()
	now := time.Now()

	op := &Operation{Name: opName, Done: false}
	b, _ := json.Marshal(ReindexMetadata{
		Progress:   0,
		StartTime:  now,
		UpdateTime: now,
	})
	op.Metadata = b
	s.store.Put(op)

	// バックグラウンド実行
	ctx, cancel := context.WithCancel(context.Background())
	s.store.RegisterCancel(opName, cancel)
	go s.runReindex(ctx, opName, publisher, now)

	writeJSON(w, http.StatusAccepted, op)
}

// 実際の長時間処理。デモ用に 1 秒 x 5 ステップで進捗更新します
func (s *Server) runReindex(ctx context.Context, opName, publisher string, start time.Time) {
	const steps = 5

	for i := 1; i <= steps; i++ {
		select {
		case <-ctx.Done():
			// gRPC code 1 = CANCELLED
			s.store.Fail(opName, 1, "operation cancelled by user")
			return
		case <-time.After(1 * time.Second):
		}

		s.store.UpdateMetadata(opName, ReindexMetadata{
			Progress:   i * 100 / steps,
			StartTime:  start,
			UpdateTime: time.Now(),
		})
	}

	s.store.Complete(opName, ReindexResponse{
		Publisher:    publisher,
		BooksIndexed: 42,
	})
}

// GET /v1/operations/{id}
// クライアントはこれをポーリングして done=true になるのを待ちます
func (s *Server) GetOperation(w http.ResponseWriter, r *http.Request) {
	name := "operations/" + chi.URLParam(r, "id")
	op, ok := s.store.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	writeJSON(w, http.StatusOK, op)
}

// POST /v1/operations/{id}:cancel
func (s *Server) CancelOperation(w http.ResponseWriter, r *http.Request) {
	name := "operations/" + chi.URLParam(r, "id")
	if !s.store.Cancel(name) {
		writeError(w, http.StatusNotFound, "operation not found or already finished")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// -----------------------------------------------------------------------------
// utility
// -----------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": msg,
		},
	})
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// -----------------------------------------------------------------------------
// main
// -----------------------------------------------------------------------------

func main() {
	srv := &Server{store: NewOperationStore()}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Post("/v1/publishers/{publisher}/books:reindex", srv.ReindexBooks)
	r.Get("/v1/operations/{id}", srv.GetOperation)
	r.Post("/v1/operations/{id}:cancel", srv.CancelOperation)

	addr := ":8080"
	fmt.Printf("listening on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, r))
}
