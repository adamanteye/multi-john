package howdy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	sigsYaml "sigs.k8s.io/yaml"
)

const (
	appName         = "john"
	workerComponent = "worker"
	workerName      = "worker"
	inputVolumeName = "input"
	workVolumeName  = "work"
	runIDLabel      = "john/run-id"
)

type Controller struct {
	client kubernetes.Interface
	config ControllerConfig
	log    *zap.SugaredLogger
}

type ControllerConfig struct {
	Namespace              string
	Image                  string
	ImagePullPolicy        string
	EtcdEndpoint           string
	JohnPath               string
	InputPath              string
	InputFile              string
	WorkPath               string
	WorkPVCName            string
	Instance               string
	LogLevel               string
	DefaultJohnFlags       string
	DefaultTotalNodes      int32
	RequestCPU             string
	RequestMemory          string
	LimitCPU               string
	LimitMemory            string
	WorkerPodTemplatePatch string
}

type CreateJobRequest struct {
	JobYAML string `json:"jobYAML"`
}

type submittedObjects struct {
	Secrets []corev1.Secret
	Job     batchv1.Job
}

type CreatedJob struct {
	RunID      string `json:"runID"`
	JobName    string `json:"jobName"`
	SecretName string `json:"secretName"`
}

type JobSummary struct {
	Name        string     `json:"name"`
	RunID       string     `json:"runID"`
	Active      int32      `json:"active"`
	Succeeded   int32      `json:"succeeded"`
	Failed      int32      `json:"failed"`
	Completions int32      `json:"completions"`
	Parallelism int32      `json:"parallelism"`
	CreatedAt   *time.Time `json:"createdAt,omitempty"`
}

func NewControllerFromEnv(logger *zap.Logger) (*Controller, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Controller{
		client: client,
		config: controllerConfigFromEnv(),
		log:    logger.Sugar(),
	}, nil
}

func controllerConfigFromEnv() ControllerConfig {
	return ControllerConfig{
		Namespace:              envString("JOHN_NAMESPACE", namespace()),
		Image:                  envString("JOHN_IMAGE", "john:latest"),
		ImagePullPolicy:        envString("JOHN_IMAGE_PULL_POLICY", string(corev1.PullIfNotPresent)),
		EtcdEndpoint:           envString("ETCD_ADVERTISE_CLIENT_URLS", "etcd:2379"),
		JohnPath:               envString("JOHN_BINARY_PATH", "john"),
		InputPath:              envString("JOHN_INPUT_PATH", "/input"),
		InputFile:              envString("JOHN_INPUT_FILE", "hashes"),
		WorkPath:               envString("JOHN_WORK_PATH", "/work"),
		WorkPVCName:            envString("JOHN_WORK_PVC_NAME", ""),
		Instance:               envString("JOHN_INSTANCE", ""),
		LogLevel:               envString("JOHN_LOG_LEVEL", "info"),
		DefaultJohnFlags:       envString("JOHN_DEFAULT_FLAGS", ""),
		DefaultTotalNodes:      envInt32("JOHN_DEFAULT_TOTAL_NODES", 2),
		RequestCPU:             envString("JOHN_WORKER_REQUEST_CPU", "250m"),
		RequestMemory:          envString("JOHN_WORKER_REQUEST_MEMORY", "64Mi"),
		LimitCPU:               envString("JOHN_WORKER_LIMIT_CPU", ""),
		LimitMemory:            envString("JOHN_WORKER_LIMIT_MEMORY", ""),
		WorkerPodTemplatePatch: envString("JOHN_WORKER_POD_TEMPLATE_PATCH", ""),
	}
}

func (c *Controller) CreateJob(ctx context.Context, req CreateJobRequest) (CreatedJob, error) {
	objects, err := parseSubmittedObjects(req.JobYAML)
	if err != nil {
		return CreatedJob{}, err
	}

	secretName := ""
	for _, secret := range objects.Secrets {
		namespace := c.objectNamespace(secret.Namespace)
		created, err := c.client.CoreV1().Secrets(namespace).Create(ctx, secret.DeepCopy(), metav1.CreateOptions{})
		if err != nil {
			return CreatedJob{}, err
		}
		if secretName == "" {
			secretName = created.Name
		}
	}

	namespace := c.objectNamespace(objects.Job.Namespace)
	job, err := c.client.BatchV1().Jobs(namespace).Create(ctx, objects.Job.DeepCopy(), metav1.CreateOptions{})
	if err != nil {
		return CreatedJob{}, err
	}

	runID := job.Labels[runIDLabel]
	if runID == "" {
		runID = job.Name
	}
	return CreatedJob{RunID: runID, JobName: job.Name, SecretName: secretName}, nil
}

func parseSubmittedObjects(value string) (submittedObjects, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return submittedObjects{}, fmt.Errorf("job YAML is required")
	}

	var out submittedObjects
	for _, document := range splitYAMLDocuments(value) {
		var typeMeta metav1.TypeMeta
		if err := sigsYaml.Unmarshal([]byte(document), &typeMeta); err != nil {
			return submittedObjects{}, fmt.Errorf("job YAML: %w", err)
		}
		switch typeMeta.Kind {
		case "Secret":
			if typeMeta.APIVersion != "" && typeMeta.APIVersion != "v1" {
				return submittedObjects{}, fmt.Errorf("job YAML: Secret apiVersion must be v1")
			}
			var secret corev1.Secret
			if err := sigsYaml.UnmarshalStrict([]byte(document), &secret); err != nil {
				return submittedObjects{}, fmt.Errorf("job YAML Secret: %w", err)
			}
			out.Secrets = append(out.Secrets, secret)
		case "Job":
			if typeMeta.APIVersion != "" && typeMeta.APIVersion != "batch/v1" {
				return submittedObjects{}, fmt.Errorf("job YAML: Job apiVersion must be batch/v1")
			}
			if out.Job.Name != "" || out.Job.GenerateName != "" {
				return submittedObjects{}, fmt.Errorf("job YAML: exactly one Job is supported")
			}
			if err := sigsYaml.UnmarshalStrict([]byte(document), &out.Job); err != nil {
				return submittedObjects{}, fmt.Errorf("job YAML Job: %w", err)
			}
		default:
			return submittedObjects{}, fmt.Errorf("job YAML: unsupported kind %q", typeMeta.Kind)
		}
	}
	if out.Job.Name == "" && out.Job.GenerateName == "" {
		return submittedObjects{}, fmt.Errorf("job YAML: one Job document is required")
	}
	return out, nil
}

func splitYAMLDocuments(value string) []string {
	parts := strings.Split(value, "\n---")
	documents := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.TrimPrefix(part, "---"))
		if part != "" {
			documents = append(documents, part)
		}
	}
	return documents
}

func (c *Controller) defaultCreateJobYAML() (string, error) {
	totalNodes := c.config.DefaultTotalNodes
	if totalNodes < 1 {
		totalNodes = 1
	}
	runID := "raw-sha256-batch"
	secretName := "raw-sha256-batch-in"
	mode := batchv1.IndexedCompletion
	backoffLimit := int32(0)
	labels := c.controllerLabels(runID)
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: c.config.Namespace,
			Labels:    labels,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			c.config.InputFile: "",
		},
	}
	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      runID,
			Namespace: c.config.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Completions:    &totalNodes,
			Parallelism:    &totalNodes,
			CompletionMode: &mode,
			BackoffLimit:   &backoffLimit,
		},
	}
	defaultPatch, err := podTemplatePatch(labels, c.workerContainer(c.config.DefaultJohnFlags, c.config.InputPath+"/"+c.config.InputFile, runID, totalNodes), c.workerVolumes(secretName))
	if err != nil {
		return "", err
	}
	if err := applyPodTemplatePatch(&job.Spec.Template, defaultPatch); err != nil {
		return "", fmt.Errorf("worker pod template defaults: %w", err)
	}
	if err := applyPodTemplatePatch(&job.Spec.Template, c.config.WorkerPodTemplatePatch); err != nil {
		return "", fmt.Errorf("worker pod template patch: %w", err)
	}

	secretYAML, err := sigsYaml.Marshal(secret)
	if err != nil {
		return "", err
	}
	jobYAML, err := sigsYaml.Marshal(job)
	if err != nil {
		return "", err
	}
	return "# This YAML is submitted as-is. Edit names before submitting another run.\n" + string(secretYAML) + "---\n" + string(jobYAML), nil
}

func (c *Controller) controllerLabels(runID string) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":       appName,
		"app.kubernetes.io/component":  workerComponent,
		"app.kubernetes.io/managed-by": appName,
		runIDLabel:                     runID,
	}
	if c.config.Instance != "" {
		labels["app.kubernetes.io/instance"] = c.config.Instance
	}
	return labels
}

func int32Value(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

func (c *Controller) objectNamespace(namespace string) string {
	if namespace != "" {
		return namespace
	}
	return c.config.Namespace
}

func podTemplatePatch(labels map[string]string, workerContainer corev1.Container, volumes []corev1.Volume) (string, error) {
	containerPatch := corev1.Container{
		Name:            workerContainer.Name,
		Image:           workerContainer.Image,
		ImagePullPolicy: workerContainer.ImagePullPolicy,
		Command:         workerContainer.Command,
		Args:            workerContainer.Args,
		Env:             workerContainer.Env,
		Resources:       workerContainer.Resources,
		VolumeMounts:    workerContainer.VolumeMounts,
	}
	templatePatch := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers:    []corev1.Container{containerPatch},
			Volumes:       volumes,
		},
	}
	patch, err := json.Marshal(templatePatch)
	if err != nil {
		return "", err
	}
	return string(patch), nil
}

func (c *Controller) workerContainer(johnFlags, inputFile, runID string, totalNodes int32) corev1.Container {
	volumeMounts := []corev1.VolumeMount{{Name: inputVolumeName, MountPath: c.config.InputPath, ReadOnly: true}}
	if c.config.WorkPVCName != "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: workVolumeName, MountPath: c.config.WorkPath})
	}

	return corev1.Container{
		Name:            workerName,
		Image:           c.config.Image,
		ImagePullPolicy: corev1.PullPolicy(c.config.ImagePullPolicy),
		Command:         []string{"./multijohn"},
		Args: []string{
			"--mode=worker",
			"--johnFile=" + inputFile,
			"--johnFlags=" + johnFlags,
			"--logLevel=" + c.config.LogLevel,
		},
		Env: []corev1.EnvVar{
			{Name: "ETCD_ADVERTISE_CLIENT_URLS", Value: c.config.EtcdEndpoint},
			{Name: "JOHN_PATH", Value: c.config.JohnPath},
			{Name: "TOTAL_NODES", Value: strconv.Itoa(int(totalNodes))},
			{Name: "JOHN_RUN_ID", Value: runID},
			{
				Name: "JOHN_NODE_INDEX",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.annotations['batch.kubernetes.io/job-completion-index']",
					},
				},
			},
		},
		Resources:    c.resources(),
		VolumeMounts: volumeMounts,
	}
}

func (c *Controller) workerVolumes(secretName string) []corev1.Volume {
	volumes := []corev1.Volume{
		{
			Name: inputVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: secretName},
			},
		},
	}
	if c.config.WorkPVCName != "" {
		volumes = append(volumes, corev1.Volume{
			Name: workVolumeName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: c.config.WorkPVCName,
				},
			},
		})
	}
	return volumes
}

func applyPodTemplatePatch(template *corev1.PodTemplateSpec, patch string) error {
	patch = strings.TrimSpace(patch)
	if patch == "" || patch == "{}" || patch == "null" {
		return nil
	}
	base, err := json.Marshal(template)
	if err != nil {
		return err
	}
	patched, err := strategicpatch.StrategicMergePatch(base, []byte(patch), corev1.PodTemplateSpec{})
	if err != nil {
		return err
	}
	var out corev1.PodTemplateSpec
	if err := json.Unmarshal(patched, &out); err != nil {
		return err
	}
	*template = out
	return nil
}

func (c *Controller) resources() corev1.ResourceRequirements {
	resources := corev1.ResourceRequirements{}
	if c.config.RequestCPU != "" || c.config.RequestMemory != "" {
		resources.Requests = corev1.ResourceList{}
		if c.config.RequestCPU != "" {
			resources.Requests[corev1.ResourceCPU] = resource.MustParse(c.config.RequestCPU)
		}
		if c.config.RequestMemory != "" {
			resources.Requests[corev1.ResourceMemory] = resource.MustParse(c.config.RequestMemory)
		}
	}
	if c.config.LimitCPU != "" || c.config.LimitMemory != "" {
		resources.Limits = corev1.ResourceList{}
		if c.config.LimitCPU != "" {
			resources.Limits[corev1.ResourceCPU] = resource.MustParse(c.config.LimitCPU)
		}
		if c.config.LimitMemory != "" {
			resources.Limits[corev1.ResourceMemory] = resource.MustParse(c.config.LimitMemory)
		}
	}
	return resources
}

func (c *Controller) ListJobs(ctx context.Context) ([]JobSummary, error) {
	selector := []string{
		"app.kubernetes.io/name=" + appName,
		"app.kubernetes.io/component=" + workerComponent,
	}
	if c.config.Instance != "" {
		selector = append(selector, "app.kubernetes.io/instance="+c.config.Instance)
	}
	re, err := c.client.BatchV1().Jobs(c.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: strings.Join(selector, ","),
	})
	if err != nil {
		return nil, err
	}
	jobs := make([]JobSummary, 0, len(re.Items))
	for _, job := range re.Items {
		createdAt := job.CreationTimestamp.Time
		summary := JobSummary{
			Name:      job.Name,
			RunID:     job.Labels[runIDLabel],
			Active:    job.Status.Active,
			Succeeded: job.Status.Succeeded,
			Failed:    job.Status.Failed,
			CreatedAt: &createdAt,
		}
		if job.Spec.Completions != nil {
			summary.Completions = *job.Spec.Completions
		}
		if job.Spec.Parallelism != nil {
			summary.Parallelism = *job.Spec.Parallelism
		}
		jobs = append(jobs, summary)
	}
	return jobs, nil
}

var invalidDNSLabel = regexp.MustCompile(`[^a-z0-9-]+`)

func runID(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = invalidDNSLabel.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "run"
	}
	if len(slug) > 40 {
		slug = slug[:40]
		slug = strings.Trim(slug, "-")
	}
	return appName + "-" + slug + "-" + strings.Split(uuid.NewString(), "-")[0]
}

func namespace() string {
	if b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return strings.TrimSpace(string(b))
	}
	return "default"
}

func envString(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func envInt32(key string, fallback int32) int32 {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 32); err == nil && parsed > 0 {
			return int32(parsed)
		}
	}
	return fallback
}
