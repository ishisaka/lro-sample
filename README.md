# AIP-151 Long-running operationsのGoでのサンプル実装

## 実装

AIP-151 のエッセンスに絞った最小構成のサンプルを作りました。chi ルーター構成にしています。

### 構成のポイント

- **`Operation` 構造体**: `google.longrunning.Operation` を JSON で素直にマッピングしたもので、`name` / `metadata` / `done` / `error` / `response` の 5 フィールドを持ちます
- **`metadata_type` / `response_type` を別の型で定義**: `ReindexMetadata`(進捗情報)と `ReindexResponse`(最終結果)を分けて、AIP-151 の設計意図を表現しています
- **開始 RPC は 202 Accepted** を返し、ボディに `Operation` を入れます
- **バックグラウンド goroutine + `context.CancelFunc`** でキャンセル対応しています
- **キャンセル時は gRPC code 1 (CANCELLED) 相当**のエラーを `Operation.error` に格納します

### 初期手順

```bash
mise install
go mod tidy
go run .
```

## 動作確認

### サーバー起動

```bash
mise run server
```

### 長時間処理の確認

```bash
mise run reindex:run
# => {"name":"operations/reindex-1234567890","metadata":{"progress":10,"startTime":"2024-06-30T12:34:56Z"},"done":false}
```

### 途中キャンセル

```bash
mise run reindex:cancel
# => {"name":"operations/reindex-1234567890","metadata":{"progress":10,"startTime":"2024-06-30T12:34:56Z"},"done":true,"error":{"code":1,"message":"operation cancelled by user"}}
```

## 参考

[longrunning proto](https://github.com/googleapis/googleapis/tree/master/google/longrunning)

[Long Running Operations API](https://github.com/googleapis/google-cloud-go/tree/main/longrunning)

[longrunning package \- cloud\.google\.com/go/longrunning \- Go Packages](https://pkg.go.dev/cloud.google.com/go/longrunning)
