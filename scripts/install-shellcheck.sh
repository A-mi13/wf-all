#!/usr/bin/env bash
# Ставит shellcheck для actionlint (`./task workflows`): без него actionlint молча пропускает проверку скриптов `run:` —
# так замечания job server ушли в CI (PR #17). Ставится в .tools/bin из релиза GitHub с проверкой sha256; зовут
# scripts/bootstrap.sh и CI (repo.yml) — версия одна локально и на раннере. Уже стоит нужная версия — ничего не делает.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=0.11.0
BIN="$PWD/.tools/bin"

case "$(uname -s)-$(uname -m)" in
  MINGW* | MSYS* | CYGWIN*)
    asset="shellcheck-v$VERSION.zip"
    sum=8a4e35ab0b331c85d73567b12f2a444df187f483e5079ceffa6bda1faa2e740e
    ;;
  Linux-x86_64)
    asset="shellcheck-v$VERSION.linux.x86_64.tar.xz"
    sum=8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198
    ;;
  Linux-aarch64)
    asset="shellcheck-v$VERSION.linux.aarch64.tar.xz"
    sum=12b331c1d2db6b9eb13cfca64306b1b157a86eb69db83023e261eaa7e7c14588
    ;;
  Darwin-arm64)
    asset="shellcheck-v$VERSION.darwin.aarch64.tar.xz"
    sum=56affdd8de5527894dca6dc3d7e0a99a873b0f004d7aabc30ae407d3f48b0a79
    ;;
  Darwin-x86_64)
    asset="shellcheck-v$VERSION.darwin.x86_64.tar.xz"
    sum=3c89db4edcab7cf1c27bff178882e0f6f27f7afdf54e859fa041fca10febe4c6
    ;;
  *)
    echo "install-shellcheck: нет сборки для $(uname -s)-$(uname -m)" >&2
    exit 1
    ;;
esac

if [[ "$("$BIN/shellcheck" --version 2>/dev/null)" == *"version: $VERSION"* ]]; then
  echo "shellcheck $VERSION актуален: $BIN"
  exit 0
fi

mkdir -p "$BIN" .tools/tmp
tmp=$(mktemp -d "$PWD/.tools/tmp/shellcheck.XXXXXX")
trap 'rm -r -- "$tmp"' EXIT
curl -fsSL --retry 3 -o "$tmp/$asset" "https://github.com/koalaman/shellcheck/releases/download/v$VERSION/$asset"
echo "$sum  $tmp/$asset" | sha256sum -c --quiet -
if [[ "$asset" == *.zip ]]; then
  unzip -q "$tmp/$asset" -d "$tmp/x"
else
  mkdir "$tmp/x"
  tar -xJf "$tmp/$asset" -C "$tmp/x"
fi
src=$(find "$tmp/x" -type f \( -name shellcheck -o -name shellcheck.exe \) | head -1)
[[ -n "$src" ]] || {
  echo "install-shellcheck: в $asset нет shellcheck" >&2
  exit 1
}
cp "$src" "$BIN/"
chmod +x "$BIN/$(basename "$src")"
"$BIN/shellcheck" --version | grep -q "version: $VERSION"
echo "shellcheck $VERSION установлен: $BIN"
