// Package upgrade verifies signed release manifests and stages binaries for offline replacement.
package upgrade

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)
const maxBinarySize int64 = 512 << 20

type Manifest struct {
	Version string `json:"version"`
	OS string `json:"os"`
	Arch string `json:"arch"`
	BinarySHA256 string `json:"binary_sha256"`
	BinarySize int64 `json:"binary_size"`
	PublishedAt time.Time `json:"published_at"`
}

var ErrInvalidRelease = errors.New("release manifest or signature is invalid")

// VerifyRelease verifies the Ed25519 signature over the exact manifest bytes and the binary digest.
func VerifyRelease(manifestBytes []byte, signature string, publicKey ed25519.PublicKey, binaryPath string) (Manifest, error) {
	var m Manifest
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(publicKey) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize || !ed25519.Verify(publicKey, manifestBytes, sig) {
		return m, ErrInvalidRelease
	}
	decoder := json.NewDecoder(strings.NewReader(string(manifestBytes)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&m); err != nil || !versionPattern.MatchString(m.Version) || m.OS == "" || m.Arch == "" || m.BinarySize <= 0 || m.BinarySize > maxBinarySize || len(m.BinarySHA256) != 64 {
		return Manifest{}, ErrInvalidRelease
	}
	if m.OS != runtime.GOOS || m.Arch != runtime.GOARCH {
		return Manifest{}, fmt.Errorf("%w: release targets %s/%s, current platform is %s/%s", ErrInvalidRelease, m.OS, m.Arch, runtime.GOOS, runtime.GOARCH)
	}
	file, err := os.Open(binaryPath)
	if err != nil { return Manifest{}, err }
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, maxBinarySize+1))
	if err != nil || n != m.BinarySize || n > maxBinarySize || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(m.BinarySHA256) {
		return Manifest{}, ErrInvalidRelease
	}
	return m, nil
}

// Stage verifies a release before copying any executable into a new staging directory.
func Stage(manifestPath, signaturePath, binaryPath, publicKeyPath, destination string) (Manifest, error) {
	manifestBytes, err := os.ReadFile(manifestPath); if err != nil { return Manifest{}, err }
	signatureBytes, err := os.ReadFile(signaturePath); if err != nil { return Manifest{}, err }
	keyBytes, err := os.ReadFile(publicKeyPath); if err != nil { return Manifest{}, err }
	manifest, err := VerifyRelease(manifestBytes, string(signatureBytes), ed25519.PublicKey(keyBytes), binaryPath)
	if err != nil { return Manifest{}, err }
	if destination == "" { return Manifest{}, errors.New("staging destination is required") }
	if _, err = os.Stat(destination); err == nil { return Manifest{}, errors.New("staging destination already exists") } else if !os.IsNotExist(err) { return Manifest{}, err }
	if err = os.MkdirAll(destination, 0o700); err != nil { return Manifest{}, err }
	ok := false
	defer func(){ if !ok { _ = os.RemoveAll(destination) } }()
	if err = copyFile(binaryPath, filepath.Join(destination,"agentmesh"),0o700); err != nil { return Manifest{}, err }
	if err = os.WriteFile(filepath.Join(destination,"manifest.json"),manifestBytes,0o600); err != nil { return Manifest{}, err }
	if err = os.WriteFile(filepath.Join(destination,"signature.ed25519"),[]byte(strings.TrimSpace(string(signatureBytes))),0o600); err != nil { return Manifest{}, err }
	ok = true
	return manifest,nil
}

// ApplyOffline atomically replaces a stopped executable and preserves a timestamped rollback copy.
// The caller must explicitly confirm that the AgentMesh process has been stopped.
func ApplyOffline(stagingDir, targetPath, backupDir, publicKeyPath string, confirmStopped bool) (string,error) {
	if !confirmStopped { return "", errors.New("refusing binary replacement without --confirm-stopped") }
	manifestBytes,err:=os.ReadFile(filepath.Join(stagingDir,"manifest.json"));if err!=nil{return "",err}
	signature,err:=os.ReadFile(filepath.Join(stagingDir,"signature.ed25519"));if err!=nil{return "",err}
	key,err:=os.ReadFile(publicKeyPath);if err!=nil{return "",err}
	binaryPath:=filepath.Join(stagingDir,"agentmesh")
	if _,err=VerifyRelease(manifestBytes,string(signature),ed25519.PublicKey(key),binaryPath);err!=nil{return "",err}
	target,err:=filepath.Abs(targetPath);if err!=nil{return "",err}
	info,err:=os.Lstat(target);if err!=nil{return "",err}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink!=0 { return "",errors.New("target must be an existing regular non-symlink file") }
	if err=os.MkdirAll(backupDir,0o700);err!=nil{return "",err}
	backup:=filepath.Join(backupDir,filepath.Base(target)+"."+time.Now().UTC().Format("20060102T150405.000000000Z")+".bak")
	if err=copyFile(target,backup,0o700);err!=nil{return "",err}
	temp,err:=os.CreateTemp(filepath.Dir(target),".agentmesh-upgrade-*");if err!=nil{return "",err}
	tempPath:=temp.Name()
	defer os.Remove(tempPath)
	src,err:=os.Open(binaryPath);if err!=nil{_ = temp.Close();return "",err}
	_,copyErr:=io.Copy(temp,src);_ = src.Close()
	if copyErr!=nil{_ = temp.Close();return "",copyErr}
	if err=temp.Chmod(0o755);err!=nil{_ = temp.Close();return "",err}
	if err=temp.Sync();err!=nil{_ = temp.Close();return "",err}
	if err=temp.Close();err!=nil{return "",err}
	if err=os.Rename(tempPath,target);err!=nil{return "",err}
	return backup,nil
}

func copyFile(source,destination string,mode os.FileMode) error {
	in,err:=os.Open(source);if err!=nil{return err};defer in.Close()
	out,err:=os.OpenFile(destination,os.O_CREATE|os.O_EXCL|os.O_WRONLY,mode);if err!=nil{return err}
	_,copyErr:=io.Copy(out,in);syncErr:=out.Sync();closeErr:=out.Close()
	if copyErr!=nil{_ = os.Remove(destination);return copyErr}
	if syncErr!=nil{_ = os.Remove(destination);return syncErr}
	if closeErr!=nil{_ = os.Remove(destination);return closeErr}
	return nil
}
