// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License;
// you may not use this file except in compliance with the Elastic License.

package beat

import (
	"fmt"
	"path"
	"strings"
	"testing"

	beatv1beta1 "github.com/elastic/cloud-on-k8s/pkg/apis/beat/v1beta1"
	commonv1 "github.com/elastic/cloud-on-k8s/pkg/apis/common/v1"
	esv1 "github.com/elastic/cloud-on-k8s/pkg/apis/elasticsearch/v1"
	beatcommon "github.com/elastic/cloud-on-k8s/pkg/controller/beat/common"
	"github.com/elastic/cloud-on-k8s/pkg/controller/common/settings"
	"github.com/elastic/cloud-on-k8s/pkg/controller/common/version"
	"github.com/elastic/cloud-on-k8s/pkg/controller/kibana"
	"github.com/elastic/cloud-on-k8s/test/e2e/test"
	"github.com/elastic/cloud-on-k8s/test/e2e/test/beat"
	"github.com/elastic/cloud-on-k8s/test/e2e/test/helper"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
)

func TestFilebeatNoAutodiscoverRecipe(t *testing.T) {
	name := "fb-no-autodiscover"
	pod, loggedString := loggingTestPod(name)
	customize := func(builder beat.Builder) beat.Builder {
		return builder.
			WithRoles(beat.PSPClusterRoleName).
			WithESValidations(
				beat.HasMessageContaining(loggedString),
			)
	}

	runBeatRecipe(t, "filebeat_no_autodiscover.yaml", customize, pod)
}

func TestFilebeatAutodiscoverRecipe(t *testing.T) {
	name := "fb-autodiscover"
	pod, loggedString := loggingTestPod(name)
	customize := func(builder beat.Builder) beat.Builder {
		return builder.
			WithRoles(beat.PSPClusterRoleName).
			WithESValidations(
				beat.HasEventFromPod(pod.Name),
				beat.HasMessageContaining(loggedString),
			)
	}

	runBeatRecipe(t, "filebeat_autodiscover.yaml", customize, pod)
}

func TestFilebeatAutodiscoverByMetadataRecipe(t *testing.T) {
	name := "fb-autodiscover-meta"
	podBad, badLog := loggingTestPod(name + "-bad")
	podLabel, goodLog := loggingTestPod(name + "-label")
	podLabel.Labels["log-label"] = "true"

	customize := func(builder beat.Builder) beat.Builder {
		return builder.
			WithRoles(beat.PSPClusterRoleName, beat.AutodiscoverClusterRoleName).
			WithESValidations(
				beat.HasEventFromPod(podLabel.Name),
				beat.HasMessageContaining(goodLog),
				beat.NoMessageContaining(badLog),
			)
	}

	runBeatRecipe(t, "filebeat_autodiscover_by_metadata.yaml", customize, podLabel, podBad)
}

func TestMetricbeatHostsRecipe(t *testing.T) {
	customize := func(builder beat.Builder) beat.Builder {
		return builder.
			WithRoles(beat.PSPClusterRoleName).
			WithESValidations(
				beat.HasEvent("event.dataset:system.cpu"),
				beat.HasEvent("event.dataset:system.load"),
				beat.HasEvent("event.dataset:system.memory"),
				beat.HasEvent("event.dataset:system.network"),
				beat.HasEvent("event.dataset:system.process"),
				beat.HasEvent("event.dataset:system.process.summary"),
				beat.HasEvent("event.dataset:system.fsstat"),
			)
	}

	runBeatRecipe(t, "metricbeat_hosts.yaml", customize)
}

func TestMetricbeatStackMonitoringRecipe(t *testing.T) {
	name := "fb-autodiscover"
	pod, loggedString := loggingTestPod(name)
	customize := func(builder beat.Builder) beat.Builder {
		// update ref to monitored cluster credentials
		if strings.HasPrefix(builder.Beat.ObjectMeta.Name, "metricbeat") {
			currSecretName := builder.Beat.Spec.Deployment.PodTemplate.Spec.Containers[0].Env[1].ValueFrom.SecretKeyRef.Name
			newSecretName := strings.Replace(currSecretName, "elasticsearch", fmt.Sprintf("elasticsearch-%s", builder.Suffix), 1)
			builder.Beat.Spec.Deployment.PodTemplate.Spec.Containers[0].Env[1].ValueFrom.SecretKeyRef.Name = newSecretName
		}

		return builder.
			WithRoles(beat.PSPClusterRoleName).
			WithESValidations(
				// metricbeat validations
				// TODO: see if we can add validation for ccr, ml_job, and shard metricsets
				beat.HasMonitoringEvent("type:cluster_stats"),
				beat.HasMonitoringEvent("type:enrich_coordinator_stats"),
				// from the elasticsearch.index metricset
				beat.HasMonitoringEvent("type:index_stats"),
				beat.HasMonitoringEvent("type:index_recovery"),
				// elasticsearch.index.summary metricset
				beat.HasMonitoringEvent("type:indices_stats"),
				beat.HasMonitoringEvent("node_stats.node_master:true"),
				beat.HasMonitoringEvent("kibana_stats.kibana.status:green"),
				// filebeat validations
				beat.HasEventFromPod(pod.Name),
				beat.HasMessageContaining(loggedString),
			)
	}

	runBeatRecipe(t, "stack_monitoring.yaml", customize, pod)
}

func TestHeartbeatEsKbHealthRecipe(t *testing.T) {
	customize := func(builder beat.Builder) beat.Builder {
		cfg := settings.MustCanonicalConfig(builder.Beat.Spec.Config.Data)
		yamlBytes, err := cfg.Render()
		require.NoError(t, err)

		spec := builder.Beat.Spec
		newEsHost := fmt.Sprintf("%s.%s.svc", esv1.HTTPService(spec.ElasticsearchRef.Name), builder.Beat.Namespace)
		newKbHost := fmt.Sprintf("%s.%s.svc", kibana.HTTPService(spec.KibanaRef.Name), builder.Beat.Namespace)

		yaml := string(yamlBytes)
		yaml = strings.ReplaceAll(yaml, "elasticsearch-es-http.default.svc", newEsHost)
		yaml = strings.ReplaceAll(yaml, "kibana-kb-http.default.svc", newKbHost)

		builder.Beat.Spec.Config = &commonv1.Config{}
		err = settings.MustParseConfig([]byte(yaml)).Unpack(&builder.Beat.Spec.Config.Data)
		require.NoError(t, err)

		return builder.
			WithRoles(beat.PSPClusterRoleName).
			WithESValidations(
				beat.HasEvent("monitor.status:up"),
			)
	}

	runBeatRecipe(t, "heartbeat_es_kb_health.yaml", customize)
}

func TestAuditbeatHostsRecipe(t *testing.T) {
	if test.Ctx().Provider == "kind" {
		// kind doesn't support configuring required settings
		// see https://github.com/elastic/cloud-on-k8s/issues/3328 for more context
		t.SkipNow()
	}

	customize := func(builder beat.Builder) beat.Builder {
		return builder.
			WithRoles(beat.AuditbeatPSPClusterRoleName).
			WithESValidations(
				beat.HasEvent("event.dataset:file"),
				beat.HasEvent("event.module:file_integrity"),
			)
	}

	runBeatRecipe(t, "auditbeat_hosts.yaml", customize)
}

func TestPacketbeatDnsHttpRecipe(t *testing.T) {
	customize := func(builder beat.Builder) beat.Builder {
		if !(test.Ctx().Provider == "kind" && test.Ctx().KubernetesVersion == "1.12") {
			// there are some issues with kind 1.12 and tracking http traffic
			builder = builder.WithESValidations(beat.HasEvent("event.dataset:http"))
		}

		return builder.
			WithRoles(beat.PacketbeatPSPClusterRoleName).
			WithESValidations(
				beat.HasEvent("event.dataset:flow"),
				beat.HasEvent("event.dataset:dns"),
			)
	}

	runBeatRecipe(t, "packetbeat_dns_http.yaml", customize)
}

func TestJournalbeatHostsRecipe(t *testing.T) {
	customize := func(builder beat.Builder) beat.Builder {
		return builder.
			WithRoles(beat.JournalbeatPSPClusterRoleName)
	}

	runBeatRecipe(t, "journalbeat_hosts.yaml", customize)
}

func runBeatRecipe(
	t *testing.T,
	fileName string,
	customize func(builder beat.Builder) beat.Builder,
	additionalObjects ...runtime.Object,
) {
	filePath := path.Join("../../../config/recipes/beats", fileName)
	namespace := test.Ctx().ManagedNamespace(0)
	suffix := rand.String(4)

	transformationsWrapped := func(builder test.Builder) test.Builder {
		beatBuilder, ok := builder.(beat.Builder)
		if !ok {
			return builder
		}

		if isStackIncompatible(beatBuilder.Beat) {
			t.SkipNow()
		}

		// OpenShift requires different securityContext than provided in the recipe.
		// Skipping it altogether to reduce maintenance burden.
		if test.Ctx().Provider == "ocp" {
			t.SkipNow()
		}

		beatBuilder.Suffix = suffix

		if customize != nil {
			beatBuilder = customize(beatBuilder)
		}

		return beatBuilder.
			WithESValidations(beat.HasEventFromBeat(beatcommon.Type(beatBuilder.Beat.Spec.Type)))
	}

	helper.RunFile(t, filePath, namespace, suffix, additionalObjects, transformationsWrapped)
}

// isStackIncompatible returns true iff Beat version is higher than tested Stack version
func isStackIncompatible(beat beatv1beta1.Beat) bool {
	stackVersion := version.MustParse(test.Ctx().ElasticStackVersion)
	beatVersion := version.MustParse(beat.Spec.Version)
	return beatVersion.IsAfter(stackVersion)
}

func loggingTestPod(name string) (*corev1.Pod, string) {
	podBuilder := beat.NewPodBuilder(name)
	return &podBuilder.Pod, podBuilder.Logged
}
