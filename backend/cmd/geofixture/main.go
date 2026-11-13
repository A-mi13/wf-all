// Command geofixture — HTTPS-фикстура GeoNames для живого e2e админки (план B, Task 23; R16): отдаёт выгрузку стран
// из выдержек репозитория (backend/internal/geo/internal/source/testdata) по путям download.geonames.org —
// export/dump/<CC>.zip, export/dump/alternatenames/<CC>.zip (запись <CC>.txt и readme.txt) и
// export/dump/admin1CodesASCII.txt. Сертификат — встроенный тестовый ключ httptest (секретов нет): PEM корня пишется в
// --ca-out, воркер доверяет ему через WORKER_GEONAMES_CA_FILE. В образ не входит; внешней сети прогон не трогает.
//
//	geofixture --addr 127.0.0.1:18190 --data <testdata> --ca-out <файл PEM>
//
// Готовность — строка «geofixture: https://<адрес>» в stdout (после записи PEM) и GET /readyz.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "geofixture:", err)
		os.Exit(1)
	}
}

// readme — запись readme.txt в каждом архиве, как у настоящей выгрузки (source её пропускает).
const readme = "GeoNames export (e2e fixture): https://www.geonames.org, CC BY 4.0\n"

// countryFile — основной файл страны в каталоге выдержек: <CC>.txt; альтернативные названия — alternateNamesV2-<CC>.txt.
var countryFile = regexp.MustCompile(`^([A-Z]{2})\.txt$`)

// run поднимает фикстуру и ждёт отмены ctx.
func run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("geofixture", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", "127.0.0.1:0", "адрес")
	data := fs.String("data", "", "каталог выдержек GeoNames")
	caOut := fs.String("ca-out", "", "куда записать PEM корня")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *data == "" || *caOut == "" || fs.NArg() > 0 {
		return errors.New("нужны --data <каталог> и --ca-out <файл>, без лишних аргументов")
	}
	files, err := dump(*data)
	if err != nil {
		return err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *addr)
	if err != nil {
		return err
	}
	srv := httptest.NewUnstartedServer(handler(files))
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.StartTLS()
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(*caOut, ca, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "geofixture: %s\n", srv.URL)
	<-ctx.Done()
	return nil
}

func handler(files map[string][]byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		b, ok := files[r.URL.Path]
		if !ok || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
}

// dump — тела ответов по путям: admin1CodesASCII.txt как есть, архивы стран — из <CC>.txt и alternateNamesV2-<CC>.txt.
func dump(dir string) (map[string][]byte, error) {
	read := func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // каталог выдержек — аргумент оператора прогона
	}
	admin1, err := read("admin1CodesASCII.txt")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{"/export/dump/admin1CodesASCII.txt": admin1}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		m := countryFile.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		cc := m[1]
		places, err := read(cc + ".txt")
		if err != nil {
			return nil, err
		}
		alt, err := read("alternateNamesV2-" + cc + ".txt")
		if err != nil {
			return nil, err
		}
		if files["/export/dump/"+cc+".zip"], err = zipOf(cc+".txt", places); err != nil {
			return nil, err
		}
		if files["/export/dump/alternatenames/"+cc+".zip"], err = zipOf(cc+".txt", alt); err != nil {
			return nil, err
		}
	}
	if len(files) == 1 {
		return nil, fmt.Errorf("в %s нет ни одной страны (<CC>.txt)", dir)
	}
	return files, nil
}

// zipOf — архив выгрузки: запись entry и readme.txt.
func zipOf(entry string, body []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct {
		name string
		body []byte
	}{{entry, body}, {"readme.txt", []byte(readme)}} {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
