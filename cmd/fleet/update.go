package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

var defaultUpdateURL = "https://github.com/javimosch/fleet-cli/releases/latest/download/version-" + runtime.GOOS + "-" + runtime.GOARCH + ".json"

var updateHash12 = regexp.MustCompile(`^[0-9a-f]{12}$`)
var updateHash64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
var updateMu sync.Mutex

type updateManifest struct {
	OK       bool   `json:"ok"`
	Version  string `json:"version"`
	Download string `json:"download"`
	SHA256   string `json:"sha256,omitempty"`
}

// cmdUpdate implements cli-update-spec: compare content hashes, then verify,
// smoke-test, and atomically install a replacement binary.
func cmdUpdate(args []string) int {
	checkOnly, force := false, false
	for _, arg := range args {
		switch arg {
		case "--check":
			checkOnly = true
		case "--force":
			force = true
		default:
			failCode(80, "invalid_arguments", "usage: fleet update [--check|--force]", "fleet help-json")
		}
	}
	if checkOnly && force {
		failCode(80, "invalid_arguments", "fleet update --check and --force are mutually exclusive", "use one flag at a time")
	}

	updateMu.Lock()
	defer updateMu.Unlock()

	exe, err := binaryPath()
	if err != nil {
		failCode(100, "update_unavailable", fmt.Sprintf("resolve running binary: %v", err), "run fleet from an executable path")
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		failCode(100, "update_unavailable", fmt.Sprintf("resolve executable target: %v", err), "run fleet from an executable path")
	}
	local, err := fileHash12(exe)
	if err != nil {
		failCode(100, "update_unavailable", fmt.Sprintf("hash running binary: %v", err), "check the binary path and permissions")
	}

	manifestURL := os.Getenv("FLEET_UPDATE_URL")
	if manifestURL == "" {
		manifestURL = defaultUpdateURL
	}
	manifest, err := fetchUpdateManifest(manifestURL)
	if err != nil {
		failCode(100, "update_manifest_invalid", err.Error(), "check FLEET_UPDATE_URL and the published manifest")
	}
	if local == manifest.Version && !force {
		outputJSON(map[string]interface{}{"ok": true, "version": local, "up_to_date": true, "updated": false})
		return 0
	}
	if checkOnly {
		outputJSON(map[string]interface{}{"ok": true, "local": local, "remote": manifest.Version, "up_to_date": false, "updated": false})
		return 5
	}

	downloadURL, err := resolveUpdateURL(manifestURL, manifest.Download)
	if err != nil {
		failCode(100, "update_download_invalid", err.Error(), "publish a relative download path or absolute URL")
	}
	fmt.Fprintf(os.Stderr, "[update] %s -> %s; downloading candidate\n", local, manifest.Version)

	tmp, err := os.CreateTemp(filepath.Dir(exe), ".fleet-update-*")
	if err != nil {
		failCode(100, "update_download_failed", fmt.Sprintf("create candidate: %v", err), "check permissions in the binary directory")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := downloadUpdate(downloadURL, tmp); err != nil {
		tmp.Close()
		failCode(100, "update_download_failed", err.Error(), "retry when the artifact is reachable")
	}
	if err := tmp.Close(); err != nil {
		failCode(100, "update_download_failed", fmt.Sprintf("close candidate: %v", err), "retry the update")
	}

	fullHash, err := fileHash(tmpPath)
	if err != nil {
		failCode(100, "update_hash_failed", fmt.Sprintf("hash candidate: %v", err), "retry the update")
	}
	if fullHash[:12] != manifest.Version {
		failCode(100, "update_hash_mismatch", fmt.Sprintf("downloaded %s does not match advertised %s", fullHash[:12], manifest.Version), "republish the complete artifact")
	}
	if manifest.SHA256 != "" && fullHash != manifest.SHA256 {
		failCode(100, "update_hash_mismatch", fmt.Sprintf("downloaded full hash %s does not match advertised %s", fullHash, manifest.SHA256), "republish the matching checksum")
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		failCode(100, "update_candidate_invalid", fmt.Sprintf("make candidate executable: %v", err), "check permissions in the binary directory")
	}
	if err := probeUpdateBinary(tmpPath); err != nil {
		failCode(100, "update_smoke_failed", err.Error(), "publish a runnable fleet binary")
	}

	backup := fmt.Sprintf("%s.bak-%s-%d", exe, manifest.Version, time.Now().UnixNano())
	if err := os.Rename(exe, backup); err != nil {
		failCode(100, "update_swap_failed", fmt.Sprintf("move current binary to backup: %v", err), "check permissions in the binary directory")
	}
	if err := os.Rename(tmpPath, exe); err != nil {
		if rollbackErr := os.Rename(backup, exe); rollbackErr != nil {
			failCode(100, "update_rollback_failed", fmt.Sprintf("install candidate: %v; rollback failed: %v", err, rollbackErr), "restore the binary manually from the backup")
		}
		failCode(100, "update_swap_failed", fmt.Sprintf("install candidate: %v (rollback restored)", err), "check permissions and filesystem health")
	}

	outputJSON(map[string]interface{}{
		"ok":      true,
		"updated": true,
		"from":    local,
		"to":      manifest.Version,
		"backup":  backup,
	})
	return 0
}

func fetchUpdateManifest(rawURL string) (updateManifest, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return updateManifest{}, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return updateManifest{}, fmt.Errorf("fetch %s: HTTP %s", rawURL, resp.Status)
	}
	var manifest updateManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&manifest); err != nil {
		return updateManifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if !manifest.OK {
		return updateManifest{}, fmt.Errorf("manifest is not marked ok")
	}
	if !updateHash12.MatchString(manifest.Version) {
		return updateManifest{}, fmt.Errorf("manifest version must be 12 lowercase hex characters")
	}
	if strings.TrimSpace(manifest.Download) == "" {
		return updateManifest{}, fmt.Errorf("manifest download is required")
	}
	if manifest.SHA256 != "" && !updateHash64.MatchString(manifest.SHA256) {
		return updateManifest{}, fmt.Errorf("manifest sha256 must be 64 lowercase hex characters")
	}
	return manifest, nil
}

func resolveUpdateURL(manifestURL, download string) (string, error) {
	base, err := url.Parse(manifestURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("invalid manifest URL %q", manifestURL)
	}
	candidate, err := url.Parse(download)
	if err != nil || candidate.Scheme != "" && candidate.Host == "" {
		return "", fmt.Errorf("invalid download URL %q", download)
	}
	resolved := base.ResolveReference(candidate)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", fmt.Errorf("download URL must use http or https")
	}
	return resolved.String(), nil
}

func downloadUpdate(rawURL string, dst io.Writer) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(rawURL)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch %s: HTTP %s", rawURL, resp.Status)
	}
	if _, err := io.Copy(dst, io.LimitReader(resp.Body, 512<<20)); err != nil {
		return fmt.Errorf("write candidate: %w", err)
	}
	return nil
}

func fileHash12(path string) (string, error) {
	h, err := fileHash(path)
	if err != nil {
		return "", err
	}
	return h[:12], nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func probeUpdateBinary(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return fmt.Errorf("candidate version probe: %w", err)
	}
	var result struct {
		OK      bool   `json:"ok"`
		Tool    string `json:"tool"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return fmt.Errorf("candidate version output is not JSON: %w", err)
	}
	if !result.OK || result.Tool != "fleet" || strings.TrimSpace(result.Version) == "" {
		return fmt.Errorf("candidate version output is not a successful fleet version response")
	}
	return nil
}
