package howdy

import (
	"context"
	"os"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDefaultCreateJobYAMLRejectsInvalidPodTemplatePatch(t *testing.T) {
	controller := testController(`{"spec":{"containers":`)
	_, err := controller.defaultCreateJobYAML()
	if err == nil {
		t.Fatalf("defaultCreateJobYAML succeeded with an invalid pod template patch")
	}
}

func TestCreateJobSubmitsYAMLAsIs(t *testing.T) {
	controller := testController("")
	controller.client = fake.NewSimpleClientset()

	created, err := controller.CreateJob(context.Background(), CreateJobRequest{JobYAML: `apiVersion: v1
kind: Secret
metadata:
  name: custom-input
  namespace: default
stringData:
  hashes: |-
    hash
---
apiVersion: batch/v1
kind: Job
metadata:
  name: custom-job
  namespace: default
  labels:
    team: security
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: worker
          image: example.com/custom:test
          args:
            - --custom
`})
	if err != nil {
		t.Fatalf("CreateJob returned error: %v", err)
	}
	if created.JobName != "custom-job" {
		t.Fatalf("created job name = %q, want custom-job", created.JobName)
	}
	if created.RunID != "custom-job" {
		t.Fatalf("created run id = %q, want custom-job", created.RunID)
	}
	if created.SecretName != "custom-input" {
		t.Fatalf("created secret name = %q, want custom-input", created.SecretName)
	}

	job, err := controller.client.BatchV1().Jobs("default").Get(context.Background(), "custom-job", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get created job: %v", err)
	}
	if _, ok := job.Labels[runIDLabel]; ok {
		t.Fatalf("job labels include controller run label: %#v", job.Labels)
	}
	worker := containerByName(job.Spec.Template.Spec.Containers, workerName)
	if worker == nil {
		t.Fatalf("worker container not found")
	}
	if worker.Image != "example.com/custom:test" {
		t.Fatalf("worker image = %q, want submitted image", worker.Image)
	}
	if len(worker.Args) != 1 || worker.Args[0] != "--custom" {
		t.Fatalf("worker args = %#v, want submitted args", worker.Args)
	}
	if len(worker.Env) != 0 {
		t.Fatalf("worker env = %#v, want submitted env only", worker.Env)
	}
	if len(job.Spec.Template.Spec.Volumes) != 0 {
		t.Fatalf("job volumes = %#v, want submitted volumes only", job.Spec.Template.Spec.Volumes)
	}
}

func TestDefaultCreateJobYAMLOmitsEmptyResourceLimits(t *testing.T) {
	controller := testController("")
	controller.config.LimitCPU = ""
	controller.config.LimitMemory = ""

	jobYAML, err := controller.defaultCreateJobYAML()
	if err != nil {
		t.Fatalf("defaultCreateJobYAML returned error: %v", err)
	}
	objects, err := parseSubmittedObjects(jobYAML)
	if err != nil {
		t.Fatalf("parseSubmittedObjects returned error: %v", err)
	}

	worker := containerByName(objects.Job.Spec.Template.Spec.Containers, workerName)
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

func TestDefaultCreateJobYAMLIncludesSecretJobDefaultsAndPatch(t *testing.T) {
	controller := testController(`{
		"metadata": {
			"annotations": {
				"example.com/template": "patched"
			}
		},
		"spec": {
			"nodeSelector": {
				"zone": "template"
			}
		}
	}`)
	controller.config.DefaultJohnFlags = "--format=raw-sha256"

	jobYAML, err := controller.defaultCreateJobYAML()
	if err != nil {
		t.Fatalf("defaultCreateJobYAML returned error: %v", err)
	}
	objects, err := parseSubmittedObjects(jobYAML)
	if err != nil {
		t.Fatalf("parseSubmittedObjects returned error: %v\n%s", err, jobYAML)
	}
	if len(objects.Secrets) != 1 {
		t.Fatalf("got %d secrets, want 1", len(objects.Secrets))
	}
	secret := objects.Secrets[0]
	if secret.Name != "raw-sha256-batch-in" {
		t.Fatalf("secret name = %q, want raw-sha256-batch-in", secret.Name)
	}
	if secret.StringData["hashes"] != "" {
		t.Fatalf("default secret hashes = %q, want empty", secret.StringData["hashes"])
	}
	if objects.Job.Name != "raw-sha256-batch" {
		t.Fatalf("default job name = %q, want raw-sha256-batch", objects.Job.Name)
	}
	if got := int32Value(objects.Job.Spec.Completions); got != 5 {
		t.Fatalf("default completions = %d, want 5", got)
	}
	if got := objects.Job.Spec.Template.Annotations["example.com/template"]; got != "patched" {
		t.Fatalf("default template annotation = %q, want patched", got)
	}
	if got := objects.Job.Spec.Template.Spec.NodeSelector["zone"]; got != "template" {
		t.Fatalf("default node selector zone = %q, want template", got)
	}
	worker := containerByName(objects.Job.Spec.Template.Spec.Containers, workerName)
	if worker == nil {
		t.Fatalf("worker container not found")
	}
	if worker.Image != controller.config.Image {
		t.Fatalf("worker image = %q, want %q", worker.Image, controller.config.Image)
	}
	if !strings.Contains(strings.Join(worker.Args, "\n"), "--johnFlags=--format=raw-sha256") {
		t.Fatalf("worker args = %#v, want default john flags", worker.Args)
	}
	if volume := volumeByName(objects.Job.Spec.Template.Spec.Volumes, inputVolumeName); volume == nil || volume.Secret == nil || volume.Secret.SecretName != "raw-sha256-batch-in" {
		t.Fatalf("input volume = %#v, want secret raw-sha256-batch-in", volume)
	}
}

func TestParseSubmittedObjectsRejectsInvalidShape(t *testing.T) {
	if _, err := parseSubmittedObjects("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: unsupported\n"); err == nil {
		t.Fatalf("parseSubmittedObjects succeeded with an unsupported kind")
	}
	if _, err := parseSubmittedObjects("apiVersion: v1\nkind: Secret\nmetadata:\n  name: only-secret\n"); err == nil {
		t.Fatalf("parseSubmittedObjects succeeded without a Job")
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
			DefaultTotalNodes:      5,
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
