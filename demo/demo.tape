# Shaktiman demo — the GIF embedded on shaktiman.dev and in the README.
#
# Re-record from the repo root:
#
#   go build -tags "sqlite_fts5 sqlite bruteforce hnsw" -o shaktiman ./cmd/shaktiman
#   shaktiman index .
#   # For hybrid (semantic) search in the recording, Ollama must be running
#   # with the embedding model pulled, and embeddings generated:
#   #   ollama pull nomic-embed-text
#   #   shaktiman reindex . --force --embed
#   PATH="$PWD:$PATH" vhs demo/demo.tape   # Require checks PATH before the tape runs

Output website/static/img/demo.gif

Require shaktiman

Set Shell "bash"
Set FontSize 15
Set Width 1300
Set Height 720
Set Padding 24
Set Theme "TokyoNight"
Set TypingSpeed 35ms

Hide
Type `export PATH="$PWD:$PATH"; clear`
Enter
Show

Type "shaktiman index ."
Sleep 400ms
Enter
Sleep 2s

Type `shaktiman search "where does the file watcher trigger reindexing" --root . --format text`
Sleep 400ms
Enter
Sleep 3.5s

Type "shaktiman deps startWatcher --root . --format text"
Sleep 400ms
Enter
Sleep 2.5s

Type `shaktiman context "how chunks are packed into a token budget" --root . --budget 4000 --format text | head -18`
Sleep 400ms
Enter
Sleep 6s
