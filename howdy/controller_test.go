package howdy

import (
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestJobSpecMountsWorkPVCAndAppliesPodTemplatePatch(t *testing.T) {
	controller := testController(`{
		"metadata": {
			"annotations": {
				"example.com/template": "patched"
			}
		},
		"spec": {
			"nodeSelector": {
				"zone": "template"
			},
			"affinity": {
				"nodeAffinity": {
					"requiredDuringSchedulingIgnoredDuringExecution": {
						"nodeSelectorTerms": [
							{
								"matchExpressions": [
									{"key": "disk", "operator": "In", "values": ["fast"]}
								]
							}
						]
					}
				}
			},
			"containers": [
				{
					"name": "worker",
					"env": [
						{"name": "TOTAL_NODES", "value": "999"},
						{"name": "EXTRA_ENV", "value": "1"}
					],
					"volumeMounts": [
						{"name": "scratch", "mountPath": "/scratch"}
					]
				}
			],
			"volumes": [
				{"name": "scratch", "emptyDir": {}}
			]
		}
	}`)
	job, err := controller.jobSpec(
		CreateJobRequest{
			JohnFlags:    "--format=raw-sha256",
			NodeSelector: map[string]string{"zone": "request", "nodepool": "cpu"},
		},
		"job-name",
		"hash-secret",
		"run-id",
		5,
		5,
		map[string]string{runIDLabel: "run-id"},
	)
	if err != nil {
		t.Fatalf("jobSpec returned error: %v", err)
	}

	template := job.Spec.Template
	if got := template.Annotations["example.com/template"]; got != "patched" {
		t.Fatalf("template annotation = %q, want patched", got)
	}
	if template.Spec.NodeSelector["zone"] != "request" {
		t.Fatalf("request node selector should override template selector")
	}
	if template.Spec.NodeSelector["nodepool"] != "cpu" {
		t.Fatalf("request node selector was not merged")
	}
	if template.Spec.Affinity == nil || template.Spec.Affinity.NodeAffinity == nil {
		t.Fatalf("affinity patch was not applied")
	}

	worker := containerByName(template.Spec.Containers, workerName)
	if worker == nil {
		t.Fatalf("worker container not found")
	}
	if got := envValue(worker.Env, "TOTAL_NODES"); got != "5" {
		t.Fatalf("TOTAL_NODES = %q, want controller value 5", got)
	}
	if got := envValue(worker.Env, "EXTRA_ENV"); got != "1" {
		t.Fatalf("EXTRA_ENV = %q, want 1", got)
	}
	if mount := volumeMountByName(worker.VolumeMounts, inputVolumeName); mount == nil || mount.MountPath != "/input" || !mount.ReadOnly {
		t.Fatalf("input volume mount = %#v, want read-only /input", mount)
	}
	if mount := volumeMountByName(worker.VolumeMounts, workVolumeName); mount == nil || mount.MountPath != "/work" || mount.ReadOnly {
		t.Fatalf("work volume mount = %#v, want writable /work", mount)
	}
	if mount := volumeMountByName(worker.VolumeMounts, "scratch"); mount == nil || mount.MountPath != "/scratch" {
		t.Fatalf("scratch volume mount = %#v, want /scratch", mount)
	}
	if volume := volumeByName(template.Spec.Volumes, workVolumeName); volume == nil || volume.PersistentVolumeClaim == nil || volume.PersistentVolumeClaim.ClaimName != "john-work" {
		t.Fatalf("work volume = %#v, want PVC john-work", volume)
	}
	if volume := volumeByName(template.Spec.Volumes, "scratch"); volume == nil || volume.EmptyDir == nil {
		t.Fatalf("scratch volume = %#v, want emptyDir", volume)
	}
	if job.Spec.ActiveDeadlineSeconds != nil {
		t.Fatalf("activeDeadlineSeconds = %v, want nil", *job.Spec.ActiveDeadlineSeconds)
	}
	if job.Spec.TTLSecondsAfterFinished != nil {
		t.Fatalf("ttlSecondsAfterFinished = %v, want nil", *job.Spec.TTLSecondsAfterFinished)
	}
}

func TestJobSpecRejectsInvalidPodTemplatePatch(t *testing.T) {
	controller := testController(`{"spec":{"containers":`)
	_, err := controller.jobSpec(
		CreateJobRequest{JohnFlags: "--format=raw-sha256"},
		"job-name",
		"hash-secret",
		"run-id",
		1,
		1,
		map[string]string{runIDLabel: "run-id"},
	)
	if err == nil {
		t.Fatalf("jobSpec succeeded with an invalid pod template patch")
	}
}

func TestJobSpecOmitsEmptyResourceLimits(t *testing.T) {
	controller := testController("")
	controller.config.LimitCPU = ""
	controller.config.LimitMemory = ""

	job, err := controller.jobSpec(
		CreateJobRequest{JohnFlags: "--format=raw-sha256"},
		"job-name",
		"hash-secret",
		"run-id",
		1,
		1,
		map[string]string{runIDLabel: "run-id"},
	)
	if err != nil {
		t.Fatalf("jobSpec returned error: %v", err)
	}

	worker := containerByName(job.Spec.Template.Spec.Containers, workerName)
	if worker == nil {
		t.Fatalf("worker container not found")
	}
	if len(worker.Resources.Limits) != 0 {
		t.Fatalf("resource limits = %#v, want none", worker.Resources.Limits)
	}
	if len(worker.Resources.Requests) == 0 {
		t.Fatalf("resource requests should still be set")
	}
}

func TestListWorkReturnsTopLevelEntries(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(workDir+"/dictionary.txt", []byte("password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(workDir+"/rules", 0o700); err != nil {
		t.Fatal(err)
	}
	controller := testController("")
	controller.config.WorkPath = workDir

	listing, err := controller.ListWork()
	if err != nil {
		t.Fatalf("ListWork returned error: %v", err)
	}
	if listing.Path != workDir {
		t.Fatalf("Path = %q, want %q", listing.Path, workDir)
	}
	if len(listing.Entries) != 2 {
		t.Fatalf("got %d entries, want 2: %#v", len(listing.Entries), listing.Entries)
	}
	file := workEntryByName(listing.Entries, "dictionary.txt")
	if file == nil {
		t.Fatalf("dictionary.txt missing from %#v", listing.Entries)
	}
	if file.Directory {
		t.Fatalf("dictionary.txt marked as directory")
	}
	if file.Size != int64(len("password\n")) {
		t.Fatalf("dictionary.txt size = %d, want %d", file.Size, len("password\n"))
	}
	dir := workEntryByName(listing.Entries, "rules")
	if dir == nil || !dir.Directory {
		t.Fatalf("rules entry = %#v, want directory", dir)
	}
}

func testController(patch string) *Controller {
	return &Controller{
		config: ControllerConfig{
			Namespace:              "default",
			Image:                  "ghcr.io/adamanteye/john:test",
			ImagePullPolicy:        string(corev1.PullIfNotPresent),
			EtcdEndpoint:           "etcd:2379",
			JohnPath:               "john",
			InputPath:              "/input",
			InputFile:              "hashes",
			WorkPath:               "/work",
			WorkPVCName:            "john-work",
			LogLevel:               "debug",
			RequestCPU:             "250m",
			RequestMemory:          "64Mi",
			LimitCPU:               "500m",
			LimitMemory:            "128Mi",
			WorkerPodTemplatePatch: patch,
		},
	}
}

func workEntryByName(entries []WorkEntry, name string) *WorkEntry {
	for i := range entries {
		if entries[i].Name == name {
			return &entries[i]
		}
	}
	return nil
}

func containerByName(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

func envValue(envs []corev1.EnvVar, name string) string {
	for _, env := range envs {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}

func volumeMountByName(mounts []corev1.VolumeMount, name string) *corev1.VolumeMount {
	for i := range mounts {
		if mounts[i].Name == name {
			return &mounts[i]
		}
	}
	return nil
}

func volumeByName(volumes []corev1.Volume, name string) *corev1.Volume {
	for i := range volumes {
		if volumes[i].Name == name {
			return &volumes[i]
		}
	}
	return nil
}
