package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	modzip "golang.org/x/mod/zip"
	"gopkg.in/yaml.v3"
)

var repoLocks sync.Map

var errNotFound = errors.New("not found")
var errBadRequest = errors.New("bad request")
var errUpstream = errors.New("upstream error")

func main() {
	config, err := loadConfig(os.Getenv("CONFIG_FILE"))
	if err != nil {
		log.Fatalf("load config file: %v", err)
	}

	listenAddr := configValue(config.listenAddr, getenv("LISTEN_ADDR", ":8081"))
	cacheDir := configValue(config.cacheDir, getenv("CACHE_DIR", "/cache"))
	gitSSHCommand := configValue(config.gitSSHCommand, getenv("GIT_SSH_COMMAND", "ssh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/tmp/goproxy_known_hosts -o BatchMode=yes"))
	mappingFile := configValue(config.mappingFile, os.Getenv("MAPPING_FILE"))
	prefixMappings := config.prefixMappings
	if len(prefixMappings) == 0 {
		modulePrefix := getenv("MODULE_PREFIX", "example.com/project")
		sshPrefix := getenv("SSH_PREFIX", "ssh://git@example.com:7999/project")
		prefixMappings = append(prefixMappings, [2]string{modulePrefix, sshPrefix})
	}

	mappings, err := loadMappings(mappingFile)
	if err != nil {
		log.Fatalf("load mapping file: %v", err)
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		log.Fatalf("create cache dir: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if err := handleProxyRequest(r.Context(), w, r, prefixMappings, cacheDir, gitSSHCommand, mappings); err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, errBadRequest):
				status = http.StatusBadRequest
			case errors.Is(err, errNotFound):
				status = http.StatusNotFound
			case errors.Is(err, errUpstream):
				status = http.StatusBadGateway
			}
			log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, http.StatusText(status), status)
		}
	})

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("listening on %s", listenAddr)
	log.Fatal(server.ListenAndServe())
}

func handleProxyRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, prefixMappings [][2]string, cacheDir, gitSSHCommand string, mappings map[string][2]string) error {
	escapedModule, action, escapedVersion, ok := splitProxyPath(r.URL.Path)
	if !ok {
		return errNotFound
	}

	modulePath, err := module.UnescapePath(escapedModule)
	if err != nil {
		return fmt.Errorf("%w: bad module path: %v", errBadRequest, err)
	}
	if err := module.CheckPath(modulePath); err != nil {
		return fmt.Errorf("%w: bad module path: %v", errBadRequest, err)
	}

	gitURL, subdir, ok := mapModule(modulePath, prefixMappings, mappings)
	if !ok {
		return errNotFound
	}

	if err := validateRelativePath(subdir); err != nil {
		return fmt.Errorf("%w: bad module subdir: %v", errBadRequest, err)
	}

	mirrorDir, err := syncMirror(ctx, cacheDir, gitURL, gitSSHCommand)
	if err != nil {
		return fmt.Errorf("%w: sync git mirror %s: %v", errUpstream, gitURL, err)
	}

	switch action {
	case "list":
		return serveList(ctx, w, mirrorDir, modulePath, subdir)
	case "latest":
		return serveLatest(ctx, w, mirrorDir, modulePath, subdir)
	case "info", "mod", "zip":
		version, err := module.UnescapeVersion(escapedVersion)
		if err != nil {
			return fmt.Errorf("%w: bad version: %v", errBadRequest, err)
		}
		if err := module.Check(modulePath, version); err != nil {
			return fmt.Errorf("%w: bad module version: %v", errBadRequest, err)
		}
		return serveVersion(ctx, w, mirrorDir, modulePath, subdir, version, action)
	default:
		return errNotFound
	}
}

func splitProxyPath(urlPath string) (string, string, string, bool) {
	path := strings.TrimPrefix(urlPath, "/")
	if strings.HasSuffix(path, "/@v/list") {
		return strings.TrimSuffix(path, "/@v/list"), "list", "", true
	}
	if strings.HasSuffix(path, "/@latest") {
		return strings.TrimSuffix(path, "/@latest"), "latest", "", true
	}

	modulePart, tail, ok := strings.Cut(path, "/@v/")
	if !ok {
		return "", "", "", false
	}

	for _, suffix := range []string{".info", ".mod", ".zip"} {
		if strings.HasSuffix(tail, suffix) {
			return modulePart, strings.TrimPrefix(suffix, "."), strings.TrimSuffix(tail, suffix), true
		}
	}

	return "", "", "", false
}

func getenv(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func configValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func loadConfig(path string) (struct {
	listenAddr     string
	cacheDir       string
	gitSSHCommand  string
	mappingFile    string
	prefixMappings [][2]string
}, error) {
	var result struct {
		listenAddr     string
		cacheDir       string
		gitSSHCommand  string
		mappingFile    string
		prefixMappings [][2]string
	}

	if strings.TrimSpace(path) == "" {
		return result, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}

	var config struct {
		ListenAddr    string `yaml:"listen_addr"`
		CacheDir      string `yaml:"cache_dir"`
		GitSSHCommand string `yaml:"git_ssh_command"`
		MappingFile   string `yaml:"mapping_file"`
		Prefixes      []struct {
			ModulePrefix string `yaml:"module_prefix"`
			SSHPrefix    string `yaml:"ssh_prefix"`
		} `yaml:"prefixes"`
	}
	if err := yaml.Unmarshal(content, &config); err != nil {
		return result, err
	}

	result.listenAddr = strings.TrimSpace(config.ListenAddr)
	result.cacheDir = strings.TrimSpace(config.CacheDir)
	result.gitSSHCommand = strings.TrimSpace(config.GitSSHCommand)
	result.mappingFile = strings.TrimSpace(config.MappingFile)
	result.prefixMappings = make([][2]string, 0, len(config.Prefixes))
	for index, item := range config.Prefixes {
		modulePrefix := strings.TrimSuffix(strings.TrimSpace(item.ModulePrefix), "/")
		if modulePrefix == "" {
			return result, fmt.Errorf("prefix %d: empty module_prefix", index+1)
		}
		sshPrefix := strings.TrimSuffix(strings.TrimSpace(item.SSHPrefix), "/")
		if sshPrefix == "" {
			return result, fmt.Errorf("prefix %d: empty ssh_prefix", index+1)
		}
		result.prefixMappings = append(result.prefixMappings, [2]string{modulePrefix, sshPrefix})
	}

	return result, nil
}

func loadMappings(path string) (map[string][2]string, error) {
	mappings := map[string][2]string{}
	if strings.TrimSpace(path) == "" {
		return mappings, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(content), "\n")
	for lineNumber, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		modulePath, target, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected module=git-url[,subdir]", lineNumber+1)
		}

		modulePath = strings.TrimSpace(modulePath)
		if err := module.CheckPath(modulePath); err != nil {
			return nil, fmt.Errorf("line %d: bad module path: %w", lineNumber+1, err)
		}

		gitURL := strings.TrimSpace(target)
		subdir := ""
		if before, after, ok := strings.Cut(gitURL, ","); ok {
			gitURL = strings.TrimSpace(before)
			subdir = strings.Trim(strings.TrimSpace(after), "/")
		}
		if gitURL == "" {
			return nil, fmt.Errorf("line %d: empty git URL", lineNumber+1)
		}
		if err := validateRelativePath(subdir); err != nil {
			return nil, fmt.Errorf("line %d: bad subdir: %w", lineNumber+1, err)
		}

		mappings[modulePath] = [2]string{gitURL, subdir}
	}

	return mappings, nil
}

func mapModule(modulePath string, prefixMappings [][2]string, mappings map[string][2]string) (string, string, bool) {
	if mapped, ok := mappings[modulePath]; ok {
		return mapped[0], mapped[1], true
	}

	for _, prefixMapping := range prefixMappings {
		modulePrefix := strings.TrimSuffix(prefixMapping[0], "/")
		sshPrefix := strings.TrimSuffix(prefixMapping[1], "/")
		rest, ok := strings.CutPrefix(modulePath, modulePrefix+"/")
		if !ok || rest == "" {
			continue
		}

		parts := strings.Split(rest, "/")
		repo := parts[0]
		if repo == "" || repo == "." || repo == ".." {
			return "", "", false
		}

		repoPath := repo
		if !strings.HasSuffix(repoPath, ".git") {
			repoPath += ".git"
		}

		subdir := ""
		if len(parts) > 1 {
			subdirParts := parts[1:]
			if len(subdirParts) > 0 && isPathMajor(subdirParts[len(subdirParts)-1]) {
				subdirParts = subdirParts[:len(subdirParts)-1]
			}
			subdir = strings.Join(subdirParts, "/")
		}

		return sshPrefix + "/" + repoPath, subdir, true
	}

	return "", "", false
}

func validateRelativePath(path string) error {
	if path == "" {
		return nil
	}
	if filepath.IsAbs(path) || strings.Contains(path, `\`) || strings.Contains(path, ":") {
		return fmt.Errorf("%q is not a clean relative path", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%q is not a clean relative path", path)
		}
	}
	return nil
}

func syncMirror(ctx context.Context, cacheDir, gitURL, gitSSHCommand string) (string, error) {
	hash := sha256.Sum256([]byte(gitURL))
	mirrorDir := filepath.Join(cacheDir, hex.EncodeToString(hash[:])+".git")

	lock := lockFor(gitURL)
	lock.Lock()
	defer lock.Unlock()

	if _, err := os.Stat(filepath.Join(mirrorDir, "HEAD")); errors.Is(err, os.ErrNotExist) {
		if err := os.RemoveAll(mirrorDir); err != nil {
			return "", err
		}
		if err := runGit(ctx, "", gitSSHCommand, "clone", "--mirror", gitURL, mirrorDir); err != nil {
			return "", err
		}
		return mirrorDir, nil
	} else if err != nil {
		return "", err
	}

	if err := runGit(ctx, mirrorDir, gitSSHCommand, "fetch", "--prune", "--tags"); err != nil {
		return "", err
	}
	return mirrorDir, nil
}

func lockFor(gitURL string) *sync.Mutex {
	value, _ := repoLocks.LoadOrStore(gitURL, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func runGit(ctx context.Context, dir, gitSSHCommand string, args ...string) error {
	_, err := gitOutput(ctx, dir, gitSSHCommand, args...)
	return err
}

func gitOutput(ctx context.Context, dir, gitSSHCommand string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = gitEnv(gitSSHCommand)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	output, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return output, nil
}

func gitEnv(gitSSHCommand string) []string {
	env := os.Environ()
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	if gitSSHCommand != "" {
		env = append(env, "GIT_SSH_COMMAND="+gitSSHCommand)
	}
	return env
}

func serveList(ctx context.Context, w http.ResponseWriter, mirrorDir, modulePath, subdir string) error {
	versions, err := listVersions(ctx, mirrorDir, modulePath, subdir)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, version := range versions {
		if _, err := fmt.Fprintln(w, version); err != nil {
			return err
		}
	}
	return nil
}

func serveLatest(ctx context.Context, w http.ResponseWriter, mirrorDir, modulePath, subdir string) error {
	versions, err := listVersions(ctx, mirrorDir, modulePath, subdir)
	if err != nil {
		return err
	}

	for i := len(versions) - 1; i >= 0; i-- {
		version := versions[i]
		commit, err := resolveVersion(ctx, mirrorDir, subdir, version)
		if err != nil {
			if errors.Is(err, errNotFound) {
				continue
			}
			return err
		}
		exists, err := treeExists(ctx, mirrorDir, commit, subdir)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		return writeInfo(ctx, w, mirrorDir, modulePath, version, commit)
	}

	commit, err := headCommit(ctx, mirrorDir)
	if err != nil {
		return errNotFound
	}
	exists, err := treeExists(ctx, mirrorDir, commit, subdir)
	if err != nil {
		return err
	}
	if !exists {
		return errNotFound
	}
	version, err := pseudoVersion(ctx, mirrorDir, modulePath, commit)
	if err != nil {
		return err
	}
	return writeInfo(ctx, w, mirrorDir, modulePath, version, commit)
}

func serveVersion(ctx context.Context, w http.ResponseWriter, mirrorDir, modulePath, subdir, version, action string) error {
	commit, err := resolveVersion(ctx, mirrorDir, subdir, version)
	if err != nil {
		return err
	}

	exists, err := treeExists(ctx, mirrorDir, commit, subdir)
	if err != nil {
		return err
	}
	if !exists {
		return errNotFound
	}

	switch action {
	case "info":
		return writeInfo(ctx, w, mirrorDir, modulePath, version, commit)
	case "mod":
		return writeMod(ctx, w, mirrorDir, modulePath, commit, subdir)
	case "zip":
		return writeZip(ctx, w, mirrorDir, modulePath, version, commit, subdir)
	default:
		return errNotFound
	}
}

func listVersions(ctx context.Context, mirrorDir, modulePath, subdir string) ([]string, error) {
	output, err := gitOutput(ctx, mirrorDir, "", "for-each-ref", "--format=%(refname:strip=2)", "refs/tags")
	if err != nil {
		return nil, err
	}

	prefixed := map[string]bool{}
	plain := map[string]bool{}
	for _, tag := range strings.Split(string(output), "\n") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if subdir != "" {
			prefix := subdir + "/"
			if strings.HasPrefix(tag, prefix) {
				version := strings.TrimPrefix(tag, prefix)
				if version, ok := proxyVersionForTag(modulePath, version); ok {
					prefixed[version] = true
				}
			}
		}
		if version, ok := proxyVersionForTag(modulePath, tag); ok {
			plain[version] = true
		}
	}

	seen := map[string]bool{}
	for version := range plain {
		seen[version] = true
	}
	for version := range prefixed {
		seen[version] = true
	}

	versions := make([]string, 0, len(seen))
	for version := range seen {
		versions = append(versions, version)
	}
	semver.Sort(versions)

	filtered := versions[:0]
	for _, version := range versions {
		if _, err := resolveVersion(ctx, mirrorDir, subdir, version); err == nil {
			filtered = append(filtered, version)
		} else if !errors.Is(err, errNotFound) {
			return nil, err
		}
	}
	return filtered, nil
}

func isPathMajor(path string) bool {
	if len(path) < 2 || path[0] != 'v' {
		return false
	}
	for _, r := range path[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return path != "v0" && path != "v1"
}

func proxyVersionForTag(modulePath, tag string) (string, bool) {
	if !semver.IsValid(tag) {
		return "", false
	}
	if module.Check(modulePath, tag) == nil {
		return tag, true
	}
	if semver.Build(tag) != "" || semver.Major(tag) == "v0" || semver.Major(tag) == "v1" {
		return "", false
	}

	version := tag + "+incompatible"
	if module.Check(modulePath, version) != nil {
		return "", false
	}
	return version, true
}

func resolveVersion(ctx context.Context, mirrorDir, subdir, version string) (string, error) {
	if semver.IsValid(version) {
		for _, tag := range tagCandidates(subdir, version) {
			commit, err := revParse(ctx, mirrorDir, "refs/tags/"+tag+"^{}")
			if err != nil {
				continue
			}
			exists, err := treeExists(ctx, mirrorDir, commit, subdir)
			if err != nil {
				return "", err
			}
			if exists {
				if semver.Build(version) == "+incompatible" {
					exists, err := goModExists(ctx, mirrorDir, commit, subdir)
					if err != nil {
						return "", err
					}
					if exists {
						continue
					}
				}
				return commit, nil
			}
		}
	}

	if module.IsPseudoVersion(version) {
		revision, err := module.PseudoVersionRev(version)
		if err != nil {
			return "", err
		}
		commit, err := revParse(ctx, mirrorDir, revision+"^{commit}")
		if err != nil {
			return "", errNotFound
		}
		commitTime, err := gitCommitTime(ctx, mirrorDir, commit)
		if err != nil {
			return "", err
		}
		versionTime, err := module.PseudoVersionTime(version)
		if err != nil {
			return "", err
		}
		if !commitTime.UTC().Equal(versionTime.UTC()) {
			return "", errNotFound
		}
		return commit, nil
	}

	return "", errNotFound
}

func tagCandidates(subdir, version string) []string {
	tagVersion := version
	if semver.Build(version) == "+incompatible" {
		tagVersion = strings.TrimSuffix(version, "+incompatible")
	}
	if subdir == "" {
		return []string{tagVersion, version}
	}
	return []string{subdir + "/" + tagVersion, subdir + "/" + version, tagVersion, version}
}

func revParse(ctx context.Context, mirrorDir, revision string) (string, error) {
	output, err := gitOutput(ctx, mirrorDir, "", "rev-parse", "--verify", revision)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func headCommit(ctx context.Context, mirrorDir string) (string, error) {
	return revParse(ctx, mirrorDir, "HEAD^{commit}")
}

func pseudoVersion(ctx context.Context, mirrorDir, modulePath, commit string) (string, error) {
	if len(commit) < 12 {
		return "", errNotFound
	}
	commitTime, err := gitCommitTime(ctx, mirrorDir, commit)
	if err != nil {
		return "", err
	}

	major := "v0"
	if _, pathMajor, ok := module.SplitPathVersion(modulePath); ok && pathMajor != "" {
		major = strings.TrimPrefix(pathMajor, "/")
	}

	return module.PseudoVersion(major, "", commitTime.UTC(), commit[:12]), nil
}

func treeExists(ctx context.Context, mirrorDir, commit, subdir string) (bool, error) {
	if subdir == "" {
		return true, nil
	}

	entryType, err := gitTreeEntryType(ctx, mirrorDir, commit, subdir)
	if err != nil {
		return false, err
	}
	return entryType == "tree", nil
}

func goModExists(ctx context.Context, mirrorDir, commit, subdir string) (bool, error) {
	modPath := goModPath(subdir)
	entryType, err := gitTreeEntryType(ctx, mirrorDir, commit, modPath)
	if err != nil {
		return false, err
	}

	switch entryType {
	case "":
		return false, nil
	case "blob":
		return true, nil
	default:
		return false, fmt.Errorf("%w: %s is %s, not blob", errUpstream, modPath, entryType)
	}
}

func gitTreeEntryType(ctx context.Context, mirrorDir, commit, path string) (string, error) {
	output, err := gitOutput(ctx, mirrorDir, "", "ls-tree", commit, "--", path)
	if err != nil {
		return "", fmt.Errorf("%w: ls-tree %s: %v", errUpstream, path, err)
	}

	line := strings.TrimSpace(string(output))
	if line == "" {
		return "", nil
	}
	line, _, _ = strings.Cut(line, "\n")

	meta, _, ok := strings.Cut(line, "\t")
	if !ok {
		return "", fmt.Errorf("%w: unexpected ls-tree output for %s", errUpstream, path)
	}
	fields := strings.Fields(meta)
	if len(fields) < 2 {
		return "", fmt.Errorf("%w: unexpected ls-tree output for %s", errUpstream, path)
	}
	return fields[1], nil
}

func writeInfo(ctx context.Context, w http.ResponseWriter, mirrorDir, modulePath, version, commit string) error {
	commitTime, err := gitCommitTime(ctx, mirrorDir, commit)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(struct {
		Version string
		Time    time.Time
	}{
		Version: version,
		Time:    commitTime.UTC(),
	})
}

func gitCommitTime(ctx context.Context, mirrorDir, commit string) (time.Time, error) {
	output, err := gitOutput(ctx, mirrorDir, "", "log", "-1", "--format=%cI", commit)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(output)))
}

func writeMod(ctx context.Context, w http.ResponseWriter, mirrorDir, modulePath, commit, subdir string) error {
	modPath := goModPath(subdir)

	entryType, err := gitTreeEntryType(ctx, mirrorDir, commit, modPath)
	if err != nil {
		return fmt.Errorf("%w: check go.mod: %v", errUpstream, err)
	}

	content := []byte(fmt.Sprintf("module %s\n\ngo 1.22\n", modulePath))
	switch entryType {
	case "":
	case "blob":
		content, err = gitOutput(ctx, mirrorDir, "", "show", commit+":"+modPath)
		if err != nil {
			return fmt.Errorf("%w: read go.mod: %v", errUpstream, err)
		}
	default:
		return fmt.Errorf("%w: %s is %s, not blob", errUpstream, modPath, entryType)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err = w.Write(content)
	return err
}

func goModPath(subdir string) string {
	if subdir == "" {
		return "go.mod"
	}
	return subdir + "/go.mod"
}

func writeZip(ctx context.Context, w http.ResponseWriter, mirrorDir, modulePath, version, commit, subdir string) error {
	sourceDir, err := os.MkdirTemp("", "goproxy-src-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(sourceDir)

	if err := exportTree(ctx, mirrorDir, commit, subdir, sourceDir); err != nil {
		return err
	}

	zipFile, err := os.CreateTemp("", "goproxy-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(zipFile.Name())
	defer zipFile.Close()

	if err := modzip.CreateFromDir(zipFile, module.Version{Path: modulePath, Version: version}, sourceDir); err != nil {
		return err
	}
	if _, err := zipFile.Seek(0, io.SeekStart); err != nil {
		return err
	}

	fileInfo, err := zipFile.Stat()
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))
	_, err = io.Copy(w, zipFile)
	return err
}

func exportTree(ctx context.Context, mirrorDir, commit, subdir, targetDir string) error {
	treeish := commit
	if subdir != "" {
		treeish = commit + ":" + subdir
	}

	cmd := exec.CommandContext(ctx, "git", "archive", "--format=tar", treeish)
	cmd.Dir = mirrorDir
	cmd.Env = gitEnv("")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	extractErr := extractTar(stdout, targetDir)
	waitErr := cmd.Wait()
	if extractErr != nil {
		return extractErr
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = waitErr.Error()
		}
		return errors.New(message)
	}
	return nil
}

func extractTar(r io.Reader, targetDir string) error {
	reader := tar.NewReader(r)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		name := filepath.Clean(header.Name)
		if name == "." || filepath.IsAbs(name) || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe tar path %q", header.Name)
		}

		targetPath := filepath.Join(targetDir, name)
		if !strings.HasPrefix(targetPath, targetDir+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe tar path %q", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)&0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, reader); err != nil {
				file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, targetPath); err != nil {
				return err
			}
		}
	}
}
