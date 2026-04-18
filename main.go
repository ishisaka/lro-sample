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

// Operation は非同期操作を表現する構造体です。
type Operation struct {
	Name     string          `json:"name"`               // "operations/{id}"
	Metadata json.RawMessage `json:"metadata,omitempty"` // metadata_type
	Done     bool            `json:"done"`
	Error    *OperationError `json:"error,omitempty"`    // 失敗時のみ
	Response json.RawMessage `json:"response,omitempty"` // 成功時のみ (response_type)
}

// OperationError は操作失敗時のエラー情報を表す構造体です。
// Code フィールドはエラーコードを表します。
// Message フィールドはエラーに関する詳細なメッセージを表します。
type OperationError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// -----------------------------------------------------------------------------
// ユースケース固有の型
//   - ReindexMetadata: 進捗などをクライアントに返すための型 (metadata_type)
//   - ReindexResponse: 最終結果 (response_type)
// -----------------------------------------------------------------------------

// ReindexMetadata は再インデックス操作の進捗とタイムスタンプを保持する構造体です。
type ReindexMetadata struct {
	Progress   int       `json:"progress"` // 0-100
	StartTime  time.Time `json:"startTime"`
	UpdateTime time.Time `json:"updateTime"`
}

// ReindexResponse は再インデックス操作の結果を格納する構造体です。
// Publisher フィールドは対象の出版社名を表します。
// BooksIndexed フィールドはインデックスされた書籍の総数を表します。
type ReindexResponse struct {
	Publisher    string `json:"publisher"`
	BooksIndexed int    `json:"booksIndexed"`
}

// -----------------------------------------------------------------------------
// インメモリ Operation ストア
// 本番では Redis や RDB など永続層に格納します
// -----------------------------------------------------------------------------

// OperationStore は非同期操作の管理を行うためのデータ構造体です。
// 操作の保存、取得、更新、完了、失敗時の処理をサポートします。
// 現在の操作状態やキャンセル関数を保持します。
type OperationStore struct {
	mu      sync.RWMutex
	ops     map[string]*Operation
	cancels map[string]context.CancelFunc
}

// NewOperationStore は新しい OperationStore インスタンスを作成して返します。
// 操作とそのキャンセル関数の管理を行います。
func NewOperationStore() *OperationStore {
	return &OperationStore{
		ops:     make(map[string]*Operation),
		cancels: make(map[string]context.CancelFunc),
	}
}

// Put は指定された Operation をストアに保存します。
// 内部でロックを取得して操作をスレッドセーフに実行します。
func (s *OperationStore) Put(op *Operation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops[op.Name] = op
}

// Get は名前に関連付けられた Operation を取得します。
// 該当する Operation が存在する場合は true を返し、存在しない場合は false を返します。
func (s *OperationStore) Get(name string) (*Operation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	op, ok := s.ops[name]
	return op, ok
}

// UpdateMetadata は指定された操作のメタデータを更新します。メタデータは指定された値を JSON にシリアライズして格納します。
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

// Complete は指定された名前の操作を完了状態に設定します。操作のレスポンスデータを格納し、キャンセル関数を削除します。
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

// Fail は指定された名前の操作を失敗状態に設定します。エラーコードとメッセージを格納し、キャンセル関数を削除します。
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

// RegisterCancel は指定された名前に関連付けてキャンセル関数を登録します。スレッドセーフに操作を実行します。
func (s *OperationStore) RegisterCancel(name string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels[name] = cancel
}

// Cancel は指定された名前に関連付けられた操作をキャンセルします。スレッドセーフに操作を実行します。 Cancel は指定された名前の非同期操作をキャンセルします。キャンセルに成功した場合は true を返し、失敗した場合は false を返します。
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

// Server は非同期操作を管理するための HTTP サーバーを表す構造体です。
// OperationStore を使用して操作の保存や状態管理を行います。
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

// runReindex は再インデックス操作を非同期に実行します。進行状況を更新し、完了またはキャンセル状態を設定します。
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

// GetOperation は指定された ID に基づいて操作を取得し、操作が見つからない場合は 404 エラーを返します。
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

// CancelOperation は指定された操作 ID に基づいて非同期操作をキャンセルします。
// 操作が見つからない場合、またはすでに完了している場合は 404 エラーを返します。
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

// writeJSON はステータスコードと JSON 応答を HTTP レスポンスライターに送信するユーティリティ関数です。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError は指定されたステータスコードとエラーメッセージを JSON 形式で HTTP レスポンスとして送信します。
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": msg,
		},
	})
}

// newID は 8 バイトのランダムなバイト列を生成し、16 進数文字列にエンコードして返します。
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
