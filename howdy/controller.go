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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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
	client *kubernetes.Clientset
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
	Name         string            `json:"name"`
	Hashes       string            `json:"hashes"`
	JohnFlags    string            `json:"johnFlags"`
	TotalNodes   int32             `json:"totalNodes"`
	Parallelism  int32             `json:"parallelism"`
	NodeSelector map[string]string `json:"nodeSelector"`
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
	if strings.TrimSpace(req.Hashes) == "" {
		return CreatedJob{}, fmt.Errorf("hashes are required")
	}
	if len(req.Hashes) > 4*1024*1024 {
		return CreatedJob{}, fmt.Errorf("hash input is too large")
	}
	if strings.TrimSpace(req.JohnFlags) == "" {
		req.JohnFlags = c.config.DefaultJohnFlags
	}

	totalNodes := req.TotalNodes
	if totalNodes < 1 {
		totalNodes = c.config.DefaultTotalNodes
	}
	parallelism := req.Parallelism
	if parallelism < 1 {
		parallelism = totalNodes
	}
	if parallelism > totalNodes {
		return CreatedJob{}, fmt.Errorf("parallelism cannot exceed totalNodes")
	}

	runID := runID(req.Name)
	jobName := runID
	secretName := runID + "-in"
	labels := map[string]string{
		"app.kubernetes.io/name":       appName,
		"app.kubernetes.io/component":  workerComponent,
		"app.kubernetes.io/managed-by": appName,
		runIDLabel:                     runID,
	}
	if c.config.Instance != "" {
		labels["app.kubernetes.io/instance"] = c.config.Instance
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: c.config.Namespace,
			Labels:    labels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			c.config.InputFile: []byte(req.Hashes),
		},
	}
	createdSecret, err := c.client.CoreV1().Secrets(c.config.Namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return CreatedJob{}, err
	}

	jobSpec, err := c.jobSpec(req, jobName, secretName, runID, totalNodes, parallelism, labels)
	if err != nil {
		if deleteErr := c.client.CoreV1().Secrets(c.config.Namespace).Delete(ctx, secretName, metav1.DeleteOptions{}); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
			c.log.Error(deleteErr)
		}
		return CreatedJob{}, err
	}

	job, err := c.client.BatchV1().Jobs(c.config.Namespace).Create(ctx, jobSpec, metav1.CreateOptions{})
	if err != nil {
		if deleteErr := c.client.CoreV1().Secrets(c.config.Namespace).Delete(ctx, secretName, metav1.DeleteOptions{}); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
			c.log.Error(deleteErr)
		}
		return CreatedJob{}, err
	}

	createdSecret.OwnerReferences = []metav1.OwnerReference{
		*metav1.NewControllerRef(job, batchv1.SchemeGroupVersion.WithKind("Job")),
	}
	if _, err := c.client.CoreV1().Secrets(c.config.Namespace).Update(ctx, createdSecret, metav1.UpdateOptions{}); err != nil {
		c.log.Warnf("created job %s but could not attach owner reference to secret %s: %v", jobName, secretName, err)
	}

	return CreatedJob{RunID: runID, JobName: jobName, SecretName: secretName}, nil
}

func (c *Controller) jobSpec(req CreateJobRequest, jobName, secretName, runID string, totalNodes, parallelism int32, labels map[string]string) (*batchv1.Job, error) {
	mode := batchv1.IndexedCompletion
	backoffLimit := int32(0)
	inputFile := c.config.InputPath + "/" + c.config.InputFile
	workerContainer := c.workerContainer(req, inputFile, runID, totalNodes)
	requiredVolumes := c.workerVolumes(secretName)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: c.config.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Completions:    &totalNodes,
			Parallelism:    &parallelism,
			CompletionMode: &mode,
			BackoffLimit:   &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					NodeSelector:  req.NodeSelector,
					Containers: []corev1.Container{
						workerContainer,
					},
					Volumes: requiredVolumes,
				},
			},
		},
	}

	if err := applyPodTemplatePatch(&job.Spec.Template, c.config.WorkerPodTemplatePatch); err != nil {
		return nil, fmt.Errorf("worker pod template patch: %w", err)
	}
	if len(req.NodeSelector) > 0 {
		if job.Spec.Template.Spec.NodeSelector == nil {
			job.Spec.Template.Spec.NodeSelector = map[string]string{}
		}
		for key, value := range req.NodeSelector {
			job.Spec.Template.Spec.NodeSelector[key] = value
		}
	}
	ensureWorkerTemplate(&job.Spec.Template, labels, workerContainer, requiredVolumes)
	return job, nil
}

func (c *Controller) workerContainer(req CreateJobRequest, inputFile, runID string, totalNodes int32) corev1.Container {
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
			"--johnFlags=" + req.JohnFlags,
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

func ensureWorkerTemplate(template *corev1.PodTemplateSpec, labels map[string]string, requiredContainer corev1.Container, requiredVolumes []corev1.Volume) {
	if template.Labels == nil {
		template.Labels = map[string]string{}
	}
	for key, value := range labels {
		template.Labels[key] = value
	}
	template.Spec.RestartPolicy = corev1.RestartPolicyNever

	for _, volume := range requiredVolumes {
		upsertVolume(&template.Spec.Volumes, volume)
	}

	for i := range template.Spec.Containers {
		if template.Spec.Containers[i].Name == workerName {
			ensureWorkerContainer(&template.Spec.Containers[i], requiredContainer)
			return
		}
	}
	template.Spec.Containers = append([]corev1.Container{requiredContainer}, template.Spec.Containers...)
}

func ensureWorkerContainer(container *corev1.Container, required corev1.Container) {
	container.Name = required.Name
	container.Image = required.Image
	container.ImagePullPolicy = required.ImagePullPolicy
	container.Command = required.Command
	container.Args = required.Args
	container.Resources = required.Resources
	for _, env := range required.Env {
		upsertEnv(&container.Env, env)
	}
	for _, mount := range required.VolumeMounts {
		upsertVolumeMount(&container.VolumeMounts, mount)
	}
}

func upsertEnv(envs *[]corev1.EnvVar, required corev1.EnvVar) {
	for i := range *envs {
		if (*envs)[i].Name == required.Name {
			(*envs)[i] = required
			return
		}
	}
	*envs = append(*envs, required)
}

func upsertVolumeMount(mounts *[]corev1.VolumeMount, required corev1.VolumeMount) {
	for i := range *mounts {
		if (*mounts)[i].Name == required.Name {
			(*mounts)[i] = required
			return
		}
	}
	*mounts = append(*mounts, required)
}

func upsertVolume(volumes *[]corev1.Volume, required corev1.Volume) {
	for i := range *volumes {
		if (*volumes)[i].Name == required.Name {
			(*volumes)[i] = required
			return
		}
	}
	*volumes = append(*volumes, required)
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
