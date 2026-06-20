package main

import (
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/adamanteye/john/howdy"
	"github.com/adamanteye/john/worker"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var mode string
var logLevel string
var johnFile string
var johnFlags string

func init() {
	flag.StringVar(&mode, "mode", "worker", "mode to start in, must be worker or howdy")
	flag.StringVar(&logLevel, "logLevel", "info", "log level, info or debug")
	flag.StringVar(&johnFile, "johnFile", "hashes", "the file with hashes to process")
	flag.StringVar(&johnFlags, "johnFlags", "", "a comma-separated list of flags to pass, e.g. --format=raw-sha256,--fork=2")
}

func GetLogLevel() zapcore.Level {
	if strings.ToLower(logLevel) == "debug" {
		return zapcore.DebugLevel
	}
	return zapcore.InfoLevel
}

func main() {
	flag.Parse()
	// Logger
	logger, _ := zap.Config{
		Encoding:         "json",
		Level:            zap.NewAtomicLevelAt(GetLogLevel()),
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
		EncoderConfig: zapcore.EncoderConfig{
			MessageKey: "message",

			LevelKey:    "level",
			EncodeLevel: zapcore.CapitalLevelEncoder,

			TimeKey:    "time",
			EncodeTime: zapcore.ISO8601TimeEncoder,

			CallerKey:    "caller",
			EncodeCaller: zapcore.ShortCallerEncoder,
		},
	}.Build()
	defer logger.Sync() // flushes buffer, if any
	sugar := logger.Sugar()

	// Find etcd
	endpoint := []string{}
	if s, ok := os.LookupEnv("ETCD_ADVERTISE_CLIENT_URLS"); ok {
		endpoint = append(endpoint, strings.Split(s, ",")...)
	} else {
		endpoint = append(endpoint, "localhost:2379")
		//sugar.Panicf("found no advertised client urls for etcd")
	}

	sugar.Info("connect to etcd...")
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoint,
		DialTimeout: 10 * time.Second,
	})
	if err != nil {
		sugar.Panic(err)
	}
	defer cli.Close()

	switch mode {
	case "howdy":
		controller, err := howdy.NewControllerFromEnv(logger)
		if err != nil {
			sugar.Warnf("kubernetes controller unavailable: %v", err)
		}
		s := howdy.New(8080, logger, cli, controller)
		go s.Serve()
	case "worker":
		err := worker.New(logger, cli, johnFile, johnFlags)
		if err != nil {
			sugar.Panic(err)
		}
		sugar.Info("worker completed")
		return
	default:
		sugar.Panicf("`%v` is not a valid mode", mode)
	}

	termChan := make(chan os.Signal, 1)
	signal.Notify(termChan, syscall.SIGINT, syscall.SIGTERM)
	<-termChan
}
