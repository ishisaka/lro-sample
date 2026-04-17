# AIP-151 Long-running operationsのGoでのサンプル実装

## 実行方法

AIP-151 のエッセンスに絞った最小構成のサンプルを作りました。chi ルーター構成にしています。

### 構成のポイント

- **`Operation` 構造体**: `google.longrunning.Operation` を JSON で素直にマッピングしたもので、`name` / `metadata` / `done` / `error` / `response` の 5 フィールドを持ちます
- **`metadata_type` / `response_type` を別の型で定義**: `ReindexMetadata`(進捗情報)と `ReindexResponse`(最終結果)を分けて、AIP-151 の設計意図を表現しています
- **開始 RPC は 202 Accepted** を返し、ボディに `Operation` を入れます
- **バックグラウンド goroutine + `context.CancelFunc`** でキャンセル対応しています
- **キャンセル時は gRPC code 1 (CANCELLED) 相当**のエラーを `Operation.error` に格納します

### 起動手順

```bash
mkdir lro-sample && cd lro-sample
# main.go を配置
go mod init example.com/lro-sample
go get github.com/go-chi/chi/v5
go run .
```

### 動作確認 (curl)

**1. 再インデックス開始:**

```bash
curl -s -X POST http://localhost:8080/v1/publishers/lacroix/books:reindex | jq
```

レスポンス例:

```json
{
  "name": "operations/3f7a8b2c1d9e4f56",
  "metadata": {"progress": 0, "startTime": "...", "updateTime": "..."},
  "done": false
}
```

**2. ポーリング** (`done: true` になるまで繰り返す):

```bash
curl -s http://localhost:8080/v1/operations/3f7a8b2c1d9e4f56 | jq
```

処理中は `progress` が 20 → 40 → 60 → 80 と増えていき、5 秒後に以下のような完了レスポンスになります。

```json
{
  "name": "operations/3f7a8b2c1d9e4f56",
  "metadata": {"progress": 100, ...},
  "done": true,
  "response": {"publisher": "lacroix", "booksIndexed": 42}
}
```

**3. キャンセル**(処理中に別ターミナルから):

```bash
curl -X POST http://localhost:8080/v1/operations/3f7a8b2c1d9e4f56:cancel
```

その後 GET すると `done: true` かつ `error.code: 1` が入った状態になります。

### 実運用に向けて追加すべき点

このサンプルは学習用に大幅に簡略化しているので、本番向けには以下を検討する必要があります。

- **永続化**: プロセス再起動で全 Operation が消えるので Redis や RDB に保存する
- **`ListOperations` / `WaitOperation` / `DeleteOperation`**: AIP-151 の Operations サービス標準メソッドが未実装
- **TTL**: 完了から 30 日後など、Operation リソースの自動期限切れ
- **冪等性**: AIP-155 の `request_id` を受け取って、同じ ID での再投稿に同じ Operation を返す
- **OpenAPI 定義化**: Ishisaka さんの `oapi-codegen` ワークフローに合わせて OpenAPI で書いてからコード生成すると、型安全に管理できます
- **ワーカー分離**: goroutine で直接処理せず、実際は Pub/Sub や Cloud Tasks のようなジョブキューに投げる構成が一般的です

特に `oapi-codegen` との組み合わせで運用する場合、Operation 本体のスキーマは `$ref` で共通化して、エンドポイントごとに `metadata` と `response` を `oneOf` やラッパー型で差し替える設計にすると見通しが良くなります。

## PowerShell での動作確認

両方用意しました。PowerShell 7.x / Windows PowerShell 5.1 のどちらでも動作します。## 実行方法

先ほどの Go サーバーを `go run .` で起動した状態で、別ターミナルから実行してください。

**完了まで待つ:**

```powershell
./run-reindex.ps1
```

進捗が 20% → 40% → 60% → 80% → 100% と表示され、最後に Response の JSON が出力されます。`Write-Progress` でプログレスバーも出るので、インタラクティブに実行すると分かりやすいです。

**途中でキャンセル:**

```powershell
./cancel-reindex.ps1
```

2 秒待ってからキャンセルを送るので、40% 程度進んだところで止まり、`error.code = 1` (CANCELLED) の Operation が返ってきます。

### ポイント

両スクリプトとも `Invoke-RestMethod` を使っているため、レスポンス JSON が自動的に PSCustomObject にデコードされ、`$op.metadata.progress` のようにドットでアクセスできます。

URL 組み立てで `$($op.name):cancel` としているのは、PowerShell のスコープ修飾子(`$global:var` など)と混同されないようにするためで、`:` の前で `$()` で明示的に閉じています。

パラメータ化しているので、本番環境の検証などで `-BaseUrl https://api.example.com` のように切り替えて使うこともできます。タイムアウトやポーリング間隔も引数で調整できるようにしてあるので、サーバー側の処理時間を変えた場合でも追従できるはずです。

## シェルスクリプトでの動作確認

bash 版を作りました。`curl` + `jq` の組み合わせで、PowerShell 版と同じ動作をします。## 前提パッケージ

`curl` と `jq` が必要です。macOS なら Homebrew で、Linux ならディストロのパッケージマネージャで入ります。

```bash
# macOS
brew install jq

# Ubuntu/Debian
sudo apt install jq curl
```

### 実行方法

Go サーバー (`go run .`) を起動した状態で、別ターミナルから実行します。ダウンロード後に実行権限を付与してください。

```bash
chmod +x run-reindex.sh cancel-reindex.sh
```

**完了まで待つ:**

```bash
./run-reindex.sh
./run-reindex.sh --publisher oreilly --poll-interval 2 --timeout 120
```

**途中でキャンセル:**

```bash
./cancel-reindex.sh
./cancel-reindex.sh --cancel-after 3
```

環境変数で上書きもできます。

```bash
BASE_URL=https://api.example.com ./run-reindex.sh
```

### PowerShell 版との対応

設計思想を合わせてあるので、対応関係は以下の通りです。

| PowerShell | bash |
| --- | --- |
| `Invoke-RestMethod` | `curl -fsS` + `jq` でパース |
| `$op.metadata.progress` | `jq -r '.metadata.progress'` |
| `$ErrorActionPreference = 'Stop'` | `set -euo pipefail` |
| `Start-Sleep -Seconds 1` | `sleep 1` |
| `Write-Host -ForegroundColor Cyan` | `echo -e "\033[0;36m...\033[0m"` |
| `-Publisher oreilly` | `--publisher oreilly` |

### 細かい工夫点

`curl -fsS` の各フラグは、`-f` が HTTP エラー時に非ゼロ終了、`-s` でプログレス非表示、`-S` でエラーメッセージは出す、という組み合わせで、スクリプト用途の定番です。

`jq -r '.error // empty'` の `//` は jq の「代替値演算子」で、左辺が null か false の場合に右辺を返します。`empty` を返すと出力が空文字列になるので、bash 側で `[[ -n "$error" ]]` によるエラー有無の判定がしやすくなります。

`sleep 0.5` は GNU coreutils と BSD (macOS) の両方で小数秒をサポートしているので、現代的な環境ではそのまま動きます。もし Alpine Linux の BusyBox sleep などで動かしたい場合は、`sleep 1` に変えるか `usleep 500000` を使う形に調整してください。
