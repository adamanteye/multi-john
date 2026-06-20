package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/adamanteye/john/worker/john"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

func New(logger *zap.Logger, cli *clientv3.Client, johnFile string, johnFlags string) error {
	var totalNodes int
	if n, ok := os.LookupEnv("TOTAL_NODES"); ok {
		var err error
		totalNodes, err = strconv.Atoi(n)
		if err != nil || totalNodes < 1 {
			return fmt.Errorf("invalid TOTAL_NODES %q", n)
		}
	} else {
		logger.Sugar().Warn("TOTAL_NODES environment missing, defaulting to TOTAL_NODES=2")
		totalNodes = 2
	}

	flags := parseFlags(johnFlags)
	johnPath := "john"
	if j, ok := os.LookupEnv("JOHN_PATH"); ok {
		johnPath = j
	}

	runID, ok := os.LookupEnv("JOHN_RUN_ID")
	if !ok || runID == "" {
		return fmt.Errorf("worker requires JOHN_RUN_ID")
	}
	index, err := completionIndex()
	if err != nil {
		return err
	}
	return runIndexed(logger, cli, johnPath, johnFile, flags, runID, index+1, totalNodes)
}

func parseFlags(johnFlags string) []string {
	if strings.TrimSpace(johnFlags) == "" {
		return nil
	}
	flags := []string{}
	for _, flag := range strings.Split(johnFlags, ",") {
		if flag = strings.TrimSpace(flag); flag != "" {
			flags = append(flags, flag)
		}
	}
	return flags
}

func completionIndex() (int, error) {
	for _, key := range []string{"JOB_COMPLETION_INDEX", "JOHN_NODE_INDEX"} {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			index, err := strconv.Atoi(value)
			if err != nil || index < 0 {
				return 0, fmt.Errorf("invalid %s %q", key, value)
			}
			return index, nil
		}
	}
	return 0, fmt.Errorf("indexed worker missing JOB_COMPLETION_INDEX")
}

func runIndexed(logger *zap.Logger, cli *clientv3.Client, johnPath, johnFile string, flags []string, runID string, nodeNumber, totalNodes int) error {
	sugar := logger.Sugar()
	flags = withoutNodeFlag(flags)
	flags = append(flags,
		fmt.Sprintf("--node=%v/%v", nodeNumber, totalNodes),
	)

	cmd := john.New(johnPath, johnFile, flags, logger)
	ompThreads := ""
	if _, ok := os.LookupEnv("OMP_NUM_THREADS"); !ok {
		ompThreads = strconv.Itoa(defaultOMPThreads())
		cmd.Env = append(cmd.Env, "OMP_NUM_THREADS="+ompThreads)
	}
	statusPath := fmt.Sprintf("runs/%s/nodes/%d/status", runID, nodeNumber)
	resultsPath := fmt.Sprintf("runs/%s/nodes/%d/results", runID, nodeNumber)

	if _, err := cli.KV.Put(context.TODO(), statusPath, "running"); err != nil {
		return err
	}

	go func() {
		for msgs := range cmd.Results {
			found, err := json.Marshal(msgs)
			if err != nil {
				sugar.Error(err)
				continue
			}
			if _, err := cli.KV.Put(context.TODO(), resultsPath, string(found)); err != nil {
				sugar.Error(err)
			}
		}
	}()

	if ompThreads != "" {
		sugar.Infof("starting indexed worker run=%s node=%d/%d omp_threads=%s", runID, nodeNumber, totalNodes, ompThreads)
	} else {
		sugar.Infof("starting indexed worker run=%s node=%d/%d", runID, nodeNumber, totalNodes)
	}
	if err := cmd.Run(); err != nil {
		_, _ = cli.KV.Put(context.TODO(), statusPath, "failed: "+err.Error())
		return err
	}
	_, err := cli.KV.Put(context.TODO(), statusPath, "completed")
	return err
}

func defaultOMPThreads() int {
	capacity := len(allowedCPUs())
	if capacity == 0 {
		capacity = runtime.NumCPU()
	}
	if quota := cpuQuotaCores(); quota > 0 && quota < capacity {
		capacity = quota
	}
	if capacity < 1 {
		return 1
	}
	return capacity
}

func allowedCPUs() []int {
	for _, path := range []string{
		"/sys/fs/cgroup/cpuset.cpus.effective",
		"/sys/fs/cgroup/cpuset/cpuset.cpus",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if cpus := parseCPUSet(strings.TrimSpace(string(data))); len(cpus) > 0 {
			return cpus
		}
	}
	return nil
}

func cpuQuotaCores() int {
	data, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return 0
	}
	return parseCPUQuota(strings.TrimSpace(string(data)))
}

func parseCPUQuota(value string) int {
	fields := strings.Fields(value)
	if len(fields) != 2 || fields[0] == "max" {
		return 0
	}
	quota, err := strconv.Atoi(fields[0])
	if err != nil || quota <= 0 {
		return 0
	}
	period, err := strconv.Atoi(fields[1])
	if err != nil || period <= 0 {
		return 0
	}
	cores := quota / period
	if quota%period != 0 {
		cores++
	}
	if cores < 1 {
		return 1
	}
	return cores
}

func parseCPUSet(value string) []int {
	cpus := []int{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if start, end, ok := strings.Cut(part, "-"); ok {
			from, err := strconv.Atoi(start)
			if err != nil {
				continue
			}
			to, err := strconv.Atoi(end)
			if err != nil || to < from {
				continue
			}
			for cpu := from; cpu <= to; cpu++ {
				cpus = append(cpus, cpu)
			}
			continue
		}
		cpu, err := strconv.Atoi(part)
		if err == nil {
			cpus = append(cpus, cpu)
		}
	}
	return cpus
}

func withoutNodeFlag(flags []string) []string {
	filtered := make([]string, 0, len(flags))
	for _, flag := range flags {
		name := flag
		if key, _, ok := strings.Cut(flag, "="); ok {
			name = key
		}
		switch name {
		case "--node":
			continue
		default:
			filtered = append(filtered, flag)
		}
	}
	return filtered
}
