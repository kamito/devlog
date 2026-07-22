# devlog

コマンドを実行したディレクトリ(カレントディレクトリ)にある Markdown(`.md`)と
HTML(`.html` / `.htm`)ファイルを走査し、
**INDEX(目次)** と **詳細ページ** を表示する、ごく小さな Web サーバーです。

このファイル自体がサンプルの Markdown でもあります。`devlog` を起動すると、
INDEX にこの「devlog」というタイトルが並び、クリックするとこのページが
HTML としてレンダリングされて表示されます。

## できること

- 実行ディレクトリ(カレントディレクトリ)内の `.md` / `.html` / `.htm` ファイルを自動で走査
- 各ファイル内で **最初に出現する見出し**(HTML の場合は `<title>`、無ければ最初の `<h1>`)を
  タイトルとして INDEX に一覧表示(どちらも無ければファイル名を使用)
- INDEX のタイトルをクリックすると詳細ページへ遷移し、
  該当ファイルを [GFM](https://github.github.com/gfm/) として HTML 表示
- HTML ファイルは **別タブ(`target="_blank"`)** でそのまま表示
- ディレクトリ内の CSS・画像などの静的ファイルも `/files/{path}` で配信されるため、
  HTML からの相対パス参照(`<link rel="stylesheet" href="style.css">` など)がそのまま動作
- **GitHub 準拠**のスタイル([github-markdown-css](https://github.com/sindresorhus/github-markdown-css))と
  コードハイライト(chroma の `github` / `github-dark` 配色、ライト/ダーク自動切替)
- ファイルの追加・編集はリクエストごとに反映(再起動不要)

## 使い方

```bash
# ビルド
go build -o devlog .

# 起動(.md ファイルを置いたディレクトリで実行する)
cd /path/to/docs
devlog
# => http://localhost:8080
```

### コマンドラインオプション

| フラグ   | デフォルト | 説明                                         |
| -------- | ---------- | -------------------------------------------- |
| `-addr`  | `0.0.0.0:8080` | 待ち受けアドレス(`host:port`)          |
| `-host`  | (`-addr` のホスト) | 待ち受けホスト(`-addr` のホスト部分を上書き) |
| `-port`  | (`-addr` のポート) | 待ち受けポート(`-addr` のポート部分を上書き) |
| `-dir`   | カレントディレクトリ | 走査対象ディレクトリ(上書き用) |
| `-max-depth` | `0` | 走査するディレクトリの深さ(`0` = 無制限、`1` = 直下のみ) |
| `-exclude` | (なし) | 走査から除外するディレクトリ名/パターン(カンマ区切り) |

`-host` / `-port` を指定すると、`-addr` の該当部分だけを上書きします。

```bash
./devlog -addr :3000          # アドレスをまとめて変更
./devlog -port 3000           # ポートだけ変更
./devlog -host 127.0.0.1      # ホストだけ変更(ローカルのみ待ち受け)
./devlog -host 127.0.0.1 -port 3000
./devlog -dir ./docs          # 走査ディレクトリを上書き
```

### 走査範囲の絞り込み

```bash
./devlog -max-depth 1                    # カレントディレクトリ直下の .md のみ
./devlog -max-depth 2                    # 直下 + 1 階層下まで
./devlog -exclude node_modules,vendor    # 該当名のディレクトリを丸ごと除外
./devlog -exclude 'tmp/*'                # tmp 直下のディレクトリだけ除外
./devlog -max-depth 3 -exclude .git,dist # 併用も可
```

`-exclude` のパターンは、ディレクトリ**名**(`node_modules`)と走査起点からの
**相対パス**(`docs/tmp`)の両方に対して照合されます。`*` や `?` などの
[glob](https://pkg.go.dev/path#Match) が使えます。マッチしたディレクトリは
配下ごとスキップされます(名前が `.` で始まるディレクトリは従来どおり常に除外)。

> **補足:** 走査の起点はカレントディレクトリです。別の場所を対象にしたい
> ときは `-dir` で上書きしてください(例: `devlog -dir ./docs`)。

## ルーティング

| メソッド・パス      | 内容                                   |
| ------------------- | -------------------------------------- |
| `GET /`             | INDEX(タイトル一覧)                  |
| `GET /view/{name}`  | `{name}.md` を HTML レンダリングして表示 |
| `GET /files/{path}` | ディレクトリ内のファイルをそのまま配信(HTML・CSS・画像など) |

`/files/` は走査対象ディレクトリ内のファイルのみ配信します(パストラバーサル、
`.` で始まるディレクトリ、`-exclude` に一致するディレクトリ配下は 404)。

## Markdown の対応記法

GFM 拡張に対応しているため、以下のような記法が利用できます。

### 強調・リンク

**太字**、*斜体*、`インラインコード`、[リンク](https://example.com)。

### リスト

- 箇条書き
  - ネストした項目
1. 番号付き
2. リスト

タスクリスト(GFM):

- [x] 完了したタスク
- [ ] 未完了のタスク

### テーブル

| 左寄せ | 中央 | 右寄せ |
| :----- | :--: | -----: |
| a      |  b   |      c |

### 引用とコードブロック

> 引用ブロックの例です。

```go
package main

import "fmt"

func main() {
	fmt.Println("hello, devlog")
}
```

## ライセンス

[MIT License](LICENSE)
