package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Um app djo é o próprio binário do djo com o site (um zip) colado no fim:
//
//	[binário do djo][zip do site][tamanho do zip: 8 bytes][DJOAPP01]
//
// Ao iniciar, o djo olha o próprio arquivo; se achar o zip, vira o app.
// No primeiro uso o site é desempacotado ao lado do executável (em "<app>.d/") e
// roda de lá, então bancos, uploads e arquivos gravados ficam em pastas comuns.

const (
	appMagic    = "DJOAPP01"
	appManifest = ".djo-app.json"
	appStamp    = ".djo-version"
)

type appInfo struct {
	Name    string `json:"name"`
	Mode    string `json:"mode"` // "serve" ou "run"
	Files   int    `json:"files"`
	Version string `json:"version"`
}

// payloadOf devolve onde fica o zip dentro do arquivo f (ok=false se não houver).
func payloadOf(f *os.File) (off, size int64, ok bool) {
	st, err := f.Stat()
	if err != nil || st.Size() < 16 {
		return 0, 0, false
	}
	var tail [16]byte
	if _, err := f.ReadAt(tail[:], st.Size()-16); err != nil || string(tail[8:]) != appMagic {
		return 0, 0, false
	}
	n := int64(binary.LittleEndian.Uint64(tail[:8]))
	if n <= 0 || n > st.Size()-16 {
		return 0, 0, false
	}
	return st.Size() - 16 - n, n, true
}

// openSelf abre o zip do app embutido neste executável, se houver.
func openSelf() (*zip.Reader, *os.File, string) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, ""
	}
	f, err := os.Open(exe)
	if err != nil {
		return nil, nil, ""
	}
	off, size, ok := payloadOf(f)
	if !ok {
		f.Close()
		return nil, nil, ""
	}
	zr, err := zip.NewReader(io.NewSectionReader(f, off, size), size)
	if err != nil {
		f.Close()
		return nil, nil, ""
	}
	return zr, f, exe
}

func readManifest(zr *zip.Reader) (appInfo, bool) {
	for _, zf := range zr.File {
		if zf.Name == appManifest {
			rc, err := zf.Open()
			if err != nil {
				return appInfo{}, false
			}
			defer rc.Close()
			var info appInfo
			if json.NewDecoder(io.LimitReader(rc, 1<<16)).Decode(&info) != nil {
				return appInfo{}, false
			}
			return info, true
		}
	}
	return appInfo{}, false
}

// unpack desempacota o site em dir. Arquivos do app são sobrescritos quando a versão
// muda; o que o app criou (banco, uploads, logs) fica como está.
func unpack(zr *zip.Reader, dir, version string) error {
	if b, err := os.ReadFile(filepath.Join(dir, appStamp)); err == nil && strings.TrimSpace(string(b)) == version {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, zf := range zr.File {
		name := path.Clean("/" + zf.Name)
		if zf.Name == appManifest || strings.HasSuffix(zf.Name, "/") || name == "/" {
			continue
		}
		dest := filepath.Join(dir, filepath.FromSlash(name))
		if !strings.HasPrefix(dest, filepath.Clean(dir)+string(os.PathSeparator)) {
			return fmt.Errorf("arquivo suspeito no app: %s", zf.Name)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		tmp := dest + ".djo-new"
		out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(tmp, dest)
		}
		if err != nil {
			os.Remove(tmp)
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, appStamp), []byte(version+"\n"), 0o644)
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "-"+name || a == "--"+name || strings.HasPrefix(a, "-"+name+"=") || strings.HasPrefix(a, "--"+name+"=") {
			return true
		}
	}
	return false
}

// runEmbedded é o main de um app djo. args são os argumentos depois do nome do app.
func runEmbedded(zr *zip.Reader, f *os.File, exe string, args []string) int {
	defer f.Close()
	info, ok := readManifest(zr)
	if !ok {
		fmt.Fprintln(os.Stderr, "app djo danificado (sem manifesto)")
		return 1
	}
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Printf("%s (app djo, modo %s, %d arquivos, versão %s)\n%s\n", info.Name, info.Mode, info.Files, info.Version, versionInfo())
		return 0
	}
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintf(os.Stderr, "uso: %s [-addr host:porta] [-dev] [-data pasta]   (modo %s)\n  version   mostra a versão\nvariáveis: PORT (porta), DJO_DATA (pasta dos dados)\n", filepath.Base(exe), info.Mode)
		return 0
	}

	// -data / DJO_DATA escolhe a pasta onde o site é desempacotado e os dados ficam
	dataDir := os.Getenv("DJO_DATA")
	var rest []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-data" || args[i] == "--data":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "-data precisa de uma pasta")
				return 2
			}
			dataDir = args[i+1]
			i++
		case strings.HasPrefix(args[i], "-data="):
			dataDir = strings.TrimPrefix(args[i], "-data=")
		default:
			rest = append(rest, args[i])
		}
	}
	if dataDir == "" {
		base := strings.TrimSuffix(exe, filepath.Ext(exe))
		dataDir = base + ".d"
	}
	if err := unpack(zr, dataDir, info.Version); err != nil {
		fmt.Fprintf(os.Stderr, "não consegui preparar %s: %v\n", dataDir, err)
		return 1
	}

	if info.Mode == "run" {
		return runFile(filepath.Join(dataDir, "main.djo"))
	}
	if p := os.Getenv("PORT"); p != "" && !hasFlag(rest, "addr") {
		rest = append([]string{"-addr", ":" + p}, rest...)
	}
	return serveCmd(append(rest, dataDir))
}

// appVersion identifica o conteúdo do zip.
func appVersion(zipBytes []byte) string {
	sum := sha256.Sum256(zipBytes)
	return hex.EncodeToString(sum[:])[:12]
}
