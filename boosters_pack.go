package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Distribuição de boosters: um booster viaja como um .tar.gz da pasta dele, e um
// registro é uma pasta (ou um site) com o indice.json e os arquivos:
//
//	registro/indice.json
//	registro/markdown/1.1.0/markdown-1.1.0.tar.gz
//
// O índice guarda o SHA256 de cada arquivo; quem baixa confere antes de instalar.

const (
	indexFile     = "indice.json"
	indexFormat   = "djo-boosters/1"
	maxArchive    = 8 << 20 // bytes do .tar.gz
	maxArchiveN   = 300     // arquivos dentro dele
	defaultRegURL = "https://djolang.zfxsoftware.cloud/registro"
)

type regVersion struct {
	Versao string `json:"versao"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type regBooster struct {
	Nome       string       `json:"nome"`
	Descricao  string       `json:"descricao"`
	Permissoes []string     `json:"permissoes"`
	Ultima     string       `json:"ultima"`
	Versoes    []regVersion `json:"versoes"`
}

type regIndex struct {
	Formato  string       `json:"formato"`
	Boosters []regBooster `json:"boosters"`
}

// cmpVersion compara versões como 1.10.0 > 1.9.3 (partes numéricas; o resto, como texto).
func cmpVersion(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := "0", "0" // parte que falta vale zero: 1.0 = 1.0.0
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		switch {
		case ex == nil && ey == nil && nx != ny:
			if nx < ny {
				return -1
			}
			return 1
		case (ex != nil || ey != nil) && x != y:
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

var versionOK = func(v string) bool {
	if v == "" || len(v) > 32 {
		return false
	}
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '.' || r == '-' || r == '+') {
			return false
		}
	}
	return true
}

// packBooster empacota a pasta do booster num .tar.gz sempre igual para o mesmo conteúdo.
func packBooster(dir string) ([]byte, *boosterManifest, error) {
	m, err := readBoosterManifest(dir)
	if err != nil {
		return nil, nil, err
	}
	if !versionOK(m.Versao) {
		return nil, nil, fmt.Errorf("versão '%s' inválida (letras, números, '.', '-' e '+')", m.Versao)
	}
	if err := checkBoosterSource(dir); err != nil {
		return nil, nil, err
	}
	if _, err := hashBooster(dir); err != nil { // confere o tamanho e os links
		return nil, nil, err
	}
	files, _ := boosterFiles(dir)
	if len(files) > maxArchiveN {
		return nil, nil, fmt.Errorf("arquivos demais (limite %d)", maxArchiveN)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		h := &tar.Header{Name: filepath.ToSlash(rel), Mode: 0o644, Size: int64(len(data)), ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}
		if err := tw.WriteHeader(h); err != nil {
			return nil, nil, err
		}
		tw.Write(data)
	}
	if err := tw.Close(); err != nil {
		return nil, nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), m, nil
}

func sumHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// unpackBooster extrai o .tar.gz em dest, recusando tudo que não seja arquivo comum
// dentro da pasta (links, caminhos com .., tamanho ou quantidade fora do limite).
func unpackBooster(data []byte, dest string) error {
	if len(data) > maxArchive {
		return fmt.Errorf("arquivo grande demais (limite %d MB)", maxArchive>>20)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("não é um .tar.gz: %v", err)
	}
	tr := tar.NewReader(io.LimitReader(gz, maxBooster+1<<20))
	var total int64
	n := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("arquivo corrompido: %v", err)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s: só arquivos comuns são aceitos (nada de links ou dispositivos)", h.Name)
		}
		name := h.Name
		if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
			return fmt.Errorf("caminho inválido no arquivo: %q", name)
		}
		for _, seg := range strings.Split(name, "/") {
			if seg == ".." {
				return fmt.Errorf("caminho inválido no arquivo: %q", name)
			}
		}
		if n++; n > maxArchiveN {
			return fmt.Errorf("arquivos demais (limite %d)", maxArchiveN)
		}
		if total += h.Size; total > maxBooster {
			return fmt.Errorf("booster grande demais (limite %d MB)", maxBooster>>20)
		}
		to := filepath.Join(dest, filepath.FromSlash(name))
		if rel, err := filepath.Rel(dest, to); err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("caminho inválido no arquivo: %q", name)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(tr, h.Size+1))
		if err != nil || int64(len(body)) != h.Size {
			return fmt.Errorf("arquivo corrompido em %s", name)
		}
		if err := os.WriteFile(to, body, 0o644); err != nil {
			return err
		}
	}
	if n == 0 {
		return fmt.Errorf("o arquivo está vazio")
	}
	return nil
}

func readIndex(data []byte) (*regIndex, error) {
	var ix regIndex
	if err := json.Unmarshal(data, &ix); err != nil {
		return nil, fmt.Errorf("%s inválido: %v", indexFile, err)
	}
	if ix.Formato != indexFormat {
		return nil, fmt.Errorf("%s: formato '%s' desconhecido (esperava %s)", indexFile, ix.Formato, indexFormat)
	}
	return &ix, nil
}

func (ix *regIndex) find(nome string) *regBooster {
	for i := range ix.Boosters {
		if ix.Boosters[i].Nome == nome {
			return &ix.Boosters[i]
		}
	}
	return nil
}

func (b *regBooster) version(v string) *regVersion {
	for i := range b.Versoes {
		if b.Versoes[i].Versao == v {
			return &b.Versoes[i]
		}
	}
	return nil
}

// publish põe o arquivo no registro (pasta) e atualiza o índice. Uma versão publicada
// não muda: repetir com o mesmo conteúdo é inofensivo, com outro conteúdo é erro.
func publishToRegistry(reg string, m *boosterManifest, archive []byte) (string, error) {
	rel := fmt.Sprintf("%s/%s/%s-%s.tar.gz", m.Nome, m.Versao, m.Nome, m.Versao)
	sum := sumHex(archive)
	ix := &regIndex{Formato: indexFormat}
	if data, err := os.ReadFile(filepath.Join(reg, indexFile)); err == nil {
		if ix, err = readIndex(data); err != nil {
			return "", err
		}
	}
	b := ix.find(m.Nome)
	if b == nil {
		ix.Boosters = append(ix.Boosters, regBooster{Nome: m.Nome})
		b = &ix.Boosters[len(ix.Boosters)-1]
	}
	if old := b.version(m.Versao); old != nil && old.SHA256 != sum {
		return "", fmt.Errorf("%s %s já foi publicado com outro conteúdo; aumente a versão no booster.json", m.Nome, m.Versao)
	} else if old == nil {
		b.Versoes = append(b.Versoes, regVersion{Versao: m.Versao, URL: rel, SHA256: sum})
	}
	sort.Slice(b.Versoes, func(i, j int) bool { return cmpVersion(b.Versoes[i].Versao, b.Versoes[j].Versao) < 0 })
	last := b.Versoes[len(b.Versoes)-1]
	b.Ultima = last.Versao
	if last.Versao == m.Versao { // a descrição e as permissões são as da última versão
		b.Descricao = m.Descricao
		b.Permissoes = m.Permissoes
		if b.Permissoes == nil {
			b.Permissoes = []string{}
		}
	}
	sort.Slice(ix.Boosters, func(i, j int) bool { return ix.Boosters[i].Nome < ix.Boosters[j].Nome })
	to := filepath.Join(reg, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(to, archive, 0o644); err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(ix, "", "  ")
	if err := os.WriteFile(filepath.Join(reg, indexFile), append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return to, nil
}

// resolveURL resolve o endereço de um arquivo do índice (relativo ao próprio índice).
func resolveURL(indexURL, ref string) (string, error) {
	base, err := url.Parse(indexURL)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(r).String(), nil
}
