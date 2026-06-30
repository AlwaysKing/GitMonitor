package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"gtimonitor/internal/api"
	"gtimonitor/internal/config"
	"gtimonitor/internal/gitops"
	"gtimonitor/internal/scheduler"
)

func main() {
	options := loadOptions()

	store, err := config.NewStore(options.configDir)
	if err != nil {
		log.Fatalf("init config store: %v", err)
	}

	gitManager := gitops.NewManager(options.repoRoot, options.gitUserName, options.gitUserEmail)
	syncService := scheduler.NewService(store, gitManager)
	if err := syncService.Load(context.Background()); err != nil {
		log.Fatalf("load repositories: %v", err)
	}

	handler := api.NewServer(store, gitManager, syncService, options.htmlDir)
	server := &http.Server{
		Addr:              options.addr,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("server listening on %s", options.addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	syncService.Stop()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

type options struct {
	addr         string
	appRoot      string
	configDir    string
	repoRoot     string
	htmlDir      string
	gitUserName  string
	gitUserEmail string
}

func loadOptions() options {
	defaultRoot := getenv("GTI_APP_ROOT", "/app")
	defaultConfigDir := getenv("GTI_CONFIG_DIR", filepath.Join(defaultRoot, "config"))
	defaultRepoRoot := getenv("GTI_REPO_ROOT", filepath.Join(defaultRoot, "git"))
	defaultHTMLDir := getenv("GTI_HTML_DIR", filepath.Join(defaultRoot, "html"))
	defaultGitUserName := getenv("GTI_COMMIT_USER_NAME", "GitMonitor")
	defaultGitUserEmail := getenv("GTI_COMMIT_USER_EMAIL", "gitmonitor@local")
	defaultAddr := resolveAddr()

	addrFlag := flag.String("addr", defaultAddr, "HTTP listen address, for example :8080")
	portFlag := flag.String("port", "", "HTTP listen port, for example 8080")
	appRootFlag := flag.String("app-root", defaultRoot, "application root directory")
	configDirFlag := flag.String("config-dir", defaultConfigDir, "config directory path")
	repoRootFlag := flag.String("repo-root", defaultRepoRoot, "git repositories root path")
	htmlDirFlag := flag.String("html-dir", defaultHTMLDir, "frontend static files path")
	gitUserNameFlag := flag.String("git-user-name", defaultGitUserName, "default git user.name for automatic commits")
	gitUserEmailFlag := flag.String("git-user-email", defaultGitUserEmail, "default git user.email for automatic commits")
	flag.Parse()

	appRoot := *appRootFlag
	configDir := *configDirFlag
	repoRoot := *repoRootFlag
	htmlDir := *htmlDirFlag
	gitUserName := *gitUserNameFlag
	gitUserEmail := *gitUserEmailFlag
	addr := *addrFlag

	if flagPassed("app-root") {
		if !flagPassed("config-dir") {
			configDir = filepath.Join(appRoot, "config")
		}
		if !flagPassed("repo-root") {
			repoRoot = filepath.Join(appRoot, "git")
		}
		if !flagPassed("html-dir") {
			htmlDir = filepath.Join(appRoot, "html")
		}
	}

	if *portFlag != "" {
		addr = ":" + *portFlag
	}

	return options{
		addr:      addr,
		appRoot:   appRoot,
		configDir: configDir,
		repoRoot:  repoRoot,
		htmlDir:   htmlDir,
		gitUserName: gitUserName,
		gitUserEmail: gitUserEmail,
	}
}

func resolveAddr() string {
	if addr := os.Getenv("GTI_ADDR"); addr != "" {
		return addr
	}
	if port := os.Getenv("GTI_PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}

func flagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
