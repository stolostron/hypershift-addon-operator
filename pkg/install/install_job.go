package install

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	kbatch "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (c *UpgradeController) runHyperShiftInstallJob(ctx context.Context, image, mountPath string, imageStreamCMData map[string]string, args []string) (*kbatch.Job, error) {
	c.log.Info(fmt.Sprintf("HyperShift install args: %v", args))

	jobPodSpec := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:    util.ImageStreamHypershiftOperator,
				Image:   image,
				Command: []string{"hypershift", "install"},
				Args:    args,
			},
		},
		RestartPolicy:      "Never",
		ServiceAccountName: util.HypershiftInstallJobServiceAccount,
		ImagePullSecrets:   []corev1.LocalObjectReference{{Name: c.pullSecret}},
	}

	// Enable RHOBS
	if strings.EqualFold(os.Getenv("RHOBS_MONITORING"), "true") {
		jobPodSpec.Containers[0].Env = []corev1.EnvVar{
			{
				Name:  "RHOBS_MONITORING",
				Value: "1",
			},
		}
	}

	if len(imageStreamCMData) > 0 {
		imageStreamCM := &corev1.ConfigMap{
			Data: imageStreamCMData,
			ObjectMeta: metav1.ObjectMeta{
				Name:      util.HypershiftInstallJobImageStream,
				Namespace: c.addonNamespace,
			},
		}

		mutateFunc := func(configmap *corev1.ConfigMap, data map[string]string) controllerutil.MutateFn {
			return func() error {
				configmap.Data = data
				return nil
			}
		}

		if _, err := controllerutil.CreateOrUpdate(ctx, c.spokeUncachedClient, imageStreamCM, mutateFunc(imageStreamCM, imageStreamCMData)); err != nil {
			return nil, err
		}

		jobPodSpec.Volumes = []corev1.Volume{{Name: util.HypershiftInstallJobVolume,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: imageStreamCM.Name,
					},
				},
			},
		}}
		jobPodSpec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: util.HypershiftInstallJobVolume, MountPath: mountPath}}
	}

	backoffLimit := int32(0)
	activeDeadlineSeconds := int64(600)
	ttlSecondsAfterFinished := int32(172800) // 48 hours
	job := &kbatch.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: util.HypershiftInstallJobName,
			Namespace:    c.addonNamespace,
		},
		Spec: kbatch.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: jobPodSpec,
			},
			BackoffLimit:            &backoffLimit,
			ActiveDeadlineSeconds:   &activeDeadlineSeconds,
			TTLSecondsAfterFinished: &ttlSecondsAfterFinished,
		},
	}
	if err := c.spokeUncachedClient.Create(ctx, job); err != nil {
		return nil, err
	}

	c.log.Info(fmt.Sprintf("created HyperShift install job: %s", job.Name))

	return job, wait.PollImmediate(10*time.Second, 2*time.Minute, c.isInstallJobFinished(ctx, job.Name))
}

func (c *UpgradeController) isInstallJobSuccessful(ctx context.Context, jobName string) (bool, error) {
	job := &kbatch.Job{}
	jobKey := types.NamespacedName{Name: jobName, Namespace: c.addonNamespace}
	if err := c.spokeUncachedClient.Get(ctx, jobKey, job); err != nil {
		return false, err
	}

	if job.Status.Succeeded > 0 {
		return true, nil
	}

	return false, nil
}

func (c *UpgradeController) isInstallJobFinished(ctx context.Context, jobName string) wait.ConditionFunc {
	return func() (bool, error) {
		job := &kbatch.Job{}
		jobKey := types.NamespacedName{Name: jobName, Namespace: c.addonNamespace}
		if err := c.spokeUncachedClient.Get(ctx, jobKey, job); err != nil {
			return false, err
		}

		if job.Status.Failed > 0 || job.Status.Succeeded > 0 {
			return true, nil
		}

		return false, nil
	}
}
