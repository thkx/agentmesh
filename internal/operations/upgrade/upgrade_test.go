package upgrade

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"context"
	"net/http"
	"net/http/httptest"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func signedFixture(t *testing.T, dir string) (string, string, string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "agentmesh")
	payload := []byte("test executable bytes")
	if err = os.WriteFile(binary, payload, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	m := Manifest{Version: "v1.2.3", OS: runtime.GOOS, Arch: runtime.GOARCH, BinarySHA256: hex.EncodeToString(sum[:]), BinarySize: int64(len(payload)), PublishedAt: time.Now().UTC()}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "manifest.json")
	if err = os.WriteFile(manifest, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	signature := filepath.Join(dir, "signature")
	if err = os.WriteFile(signature, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, raw))), 0o600); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "public.key")
	if err = os.WriteFile(key, pub, 0o600); err != nil {
		t.Fatal(err)
	}
	return manifest, signature, binary, key
}

func TestStageAndOfflineApplyRequireStoppedConfirmation(t *testing.T) {
	dir := t.TempDir()
	manifest, sig, binary, key := signedFixture(t, dir)
	stage := filepath.Join(dir, "stage")
	if _, err := Stage(manifest, sig, binary, key, stage); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "current-agentmesh")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyOffline(stage, target, filepath.Join(dir, "backups"), key, false); err == nil {
		t.Fatal("apply without stop confirmation succeeded")
	}
	backup, err := ApplyOffline(stage, target, filepath.Join(dir, "backups"), key, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(backup); err != nil {
		t.Fatalf("rollback copy missing: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "test executable bytes" {
		t.Fatalf("binary contents=%q", got)
	}
}

func TestVerifyReleaseRejectsModifiedBinary(t *testing.T) {
	dir := t.TempDir()
	manifest, sig, binary, key := signedFixture(t, dir)
	if err := os.WriteFile(binary, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(manifest)
	signature, _ := os.ReadFile(sig)
	pub, _ := os.ReadFile(key)
	if _, err := VerifyRelease(raw, string(signature), ed25519.PublicKey(pub), binary); err == nil {
		t.Fatal("tampered binary accepted")
	}
}

func TestCheckReleaseFetchesAndVerifiesSignedMetadata(t *testing.T) {
	dir:=t.TempDir()
	manifestPath,signaturePath,_,keyPath:=signedFixture(t,dir)
	manifest,_:=os.ReadFile(manifestPath)
	signature,_:=os.ReadFile(signaturePath)
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		switch r.URL.Path{case "/manifest.json":_,_=w.Write(manifest);case "/manifest.sig":_,_=w.Write(signature);default:http.NotFound(w,r)}
	}))
	defer server.Close()
	pub,err:=os.ReadFile(keyPath);if err!=nil{t.Fatal(err)}
	result,err:=CheckRelease(context.Background(),server.URL+"/manifest.json",server.URL+"/manifest.sig",ed25519.PublicKey(pub))
	if err!=nil{t.Fatal(err)}
	if result.Version!="v1.2.3"{t.Fatalf("version=%s",result.Version)}
}

func TestCheckReleaseRejectsRemotePlainHTTP(t *testing.T) {
	if _,err:=fetchReleaseDocument(context.Background(),&http.Client{},"http://release.example/manifest");err==nil{t.Fatal("remote plain HTTP release URL accepted")}
}
