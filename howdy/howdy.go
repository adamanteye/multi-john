package howdy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

type Server struct {
	port       int
	log        *zap.SugaredLogger
	etcd       *clientv3.Client
	controller *Controller
}

type Results struct {
	Nodes map[int]Node `json:"node"`
}

type Node struct {
	Status  string   `json:"status"`
	Results []string `json:"results"`
}

type UIConfig struct {
	DefaultJobYAML string `json:"defaultJobYAML"`
}

type WorkListing struct {
	Path    string      `json:"path"`
	Entries []WorkEntry `json:"entries"`
}

type WorkEntry struct {
	Name       string     `json:"name"`
	Path       string     `json:"path"`
	Directory  bool       `json:"directory"`
	Size       int64      `json:"size"`
	ModifiedAt *time.Time `json:"modifiedAt,omitempty"`
}

func New(port int, logger *zap.Logger, etcd *clientv3.Client, controller *Controller) Server {
	return Server{
		port:       port,
		log:        logger.Sugar(),
		etcd:       etcd,
		controller: controller,
	}
}

func (s *Server) GetRun(runID string) Results {
	re, _ := s.etcd.KV.Get(context.TODO(), "runs/"+runID+"/nodes/", clientv3.WithPrefix())
	if len(re.Kvs) == 0 {
		return Results{}
	}

	r := map[int]Node{}
	for _, kv := range re.Kvs {
		key := string(kv.Key)
		value := kv.Value
		p := strings.Split(key, "/")
		if len(p) < 5 {
			continue
		}
		nn, err := strconv.Atoi(p[3])
		if err != nil {
			continue
		}
		node := r[nn]
		switch p[4] {
		case "status":
			node.Status = string(value)
		case "results":
			d := []string{}
			if err := json.Unmarshal(value, &d); err != nil {
				s.log.Error(err)
			}
			node.Results = d
		}
		r[nn] = node
	}
	return Results{Nodes: r}
}

func (s *Server) Serve() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/results", s.handleResults)
	mux.HandleFunc("/api/jobs", s.handleJobs)
	mux.HandleFunc("/api/work", s.handleWork)

	s.log.Infof("serving on port %v", s.port)
	if err := http.ListenAndServe(fmt.Sprintf(":%v", s.port), mux); err != nil {
		s.log.Error(err)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := fmt.Fprint(w, indexHTML); err != nil {
		s.log.Error(err)
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	cfg := UIConfig{}
	if s.controller != nil {
		defaultJobYAML, err := s.controller.defaultCreateJobYAML()
		if err != nil {
			s.log.Error(err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		cfg.DefaultJobYAML = defaultJobYAML
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	runID := strings.TrimSpace(r.URL.Query().Get("runID"))
	if runID != "" {
		writeJSON(w, http.StatusOK, s.GetRun(runID))
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "runID is required"})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if s.controller == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "kubernetes controller is unavailable"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		jobs, err := s.controller.ListJobs(r.Context())
		if err != nil {
			s.log.Error(err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, jobs)
	case http.MethodPost:
		defer r.Body.Close()
		var req CreateJobRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		created, err := s.controller.CreateJob(r.Context(), req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWork(w http.ResponseWriter, r *http.Request) {
	if s.controller == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "kubernetes controller is unavailable"})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	listing, err := s.controller.ListWork()
	if err != nil {
		s.log.Error(err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) ListWork() (WorkListing, error) {
	workPath := strings.TrimSpace(c.config.WorkPath)
	if workPath == "" {
		workPath = "/work"
	}
	dirEntries, err := os.ReadDir(workPath)
	if err != nil {
		return WorkListing{}, fmt.Errorf("list %s: %w", workPath, err)
	}
	entries := make([]WorkEntry, 0, len(dirEntries))
	for _, entry := range dirEntries {
		info, err := entry.Info()
		if err != nil {
			return WorkListing{}, fmt.Errorf("stat %s/%s: %w", workPath, entry.Name(), err)
		}
		modifiedAt := info.ModTime()
		entries = append(entries, WorkEntry{
			Name:       entry.Name(),
			Path:       workPath + "/" + entry.Name(),
			Directory:  entry.IsDir(),
			Size:       info.Size(),
			ModifiedAt: &modifiedAt,
		})
	}
	return WorkListing{Path: workPath, Entries: entries}, nil
}
