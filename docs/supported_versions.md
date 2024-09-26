# OCP Version Support

## MCE hub (management or hosting) Cluster
[Multi-Cluster Engine](https://docs.openshift.com/container-platform/4.17/architecture/mce-overview-ocp.html) (MCE), which is available through OpenShift's OperatorHub, requires specific OCP versions for the Management Cluster to remain in a supported state.

Each version documents its own support matrix. For example, 

- [MCE 2.7](https://access.redhat.com/articles/7086906)
- [MCE 2.6](https://access.redhat.com/articles/7073030)
- [MCE 2.5](https://access.redhat.com/articles/7056007)
- [MCE 2.4](https://access.redhat.com/articles/7027079)

As a heuristic, OCP versions MCE supports are:

- The latest, yet to be released version (N+1) of OpenShift
- The latest GA version (N) of OpenShift
- Two versions prior (N-2) to the latest GA version

Versions of MCE can also be obtained with the Red Hat Advanced Cluster Management (ACM) offering. If you are running ACM, refer to product documentation to determine the bundled MCE version.

## Hosted Clusters
The OCP version of the management cluster does not affect the OCP version of a hosted cluster you can deploy. The hypershift operator that MCE installs creates a ConfigMap called `supported-versions` into  `hypershift` namespace, which describes the range of supported OCP versions that could be deployed. 

Here is an example `supported-versions` ConfigMap:
```
apiVersion: v1
data:
    server-version: 2f6cfe21a0861dea3130f3bed0d3ae5553b8c28b
    supported-versions: '{"versions":["4.17","4.16","4.15","4.14"]}'
kind: ConfigMap
metadata:
    creationTimestamp: "2024-06-20T07:12:31Z"
    labels:
        hypershift.openshift.io/supported-versions: "true"
    name: supported-versions
    namespace: hypershift
    resourceVersion: "927029"
    uid: f6336f91-33d3-472d-b747-94abae725f70
```

It is important to note that you cannot install hosted clusters outside of this support version range. In the example above, hosted clusters using OCP release images greater than 4.17 cannot be created. If you want to deploy a higher version of OCP hosted cluster, you need to upgrade MCE to a new y-stream release to deploy a new version of the hypershift operator. Upgrading MCE to a new z-release does not upgrade the hypershift operator to the next version.  

Running the `hcp version` command will also show the OCP version support information against your KUBECONFIG that connects to the management cluster.

```
% ./hcp version
Client Version: openshift/hypershift: fe67b47fb60e483fe60e4755a02b3be393256343. Latest supported OCP: 4.17.0
Server Version: 05864f61f24a8517731664f8091cedcfc5f9b60d
Server Supports OCP Versions: 4.17, 4.16, 4.15, 4.14
```

In this example, both the hypershift operator on the management cluster and the HCP CLI client support OCP version 4.17, 4.16, 4.15, 4.14. However, the operator and the CLI are at different build levels.

The HCP CLI is available through download from MCE or https://developers.redhat.com/content-gateway/rest/browse/pub/mce/clients/hcp-cli/. 


# OCP Version Upgrades in Hosted Clusters
HyperShift enables the decoupling of upgrades between the control plane and the data plane, the worker nodes.

This allows there to be two separate procedures a cluster service provider or cluster administrator can take, giving them flexibility to manage the different components separately.

Control plane upgrades are driven by the HostedCluster custom resource, while node upgrades are driven by its respective NodePool custom resource. Both the HostedCluster and NodePool custom resources expose a `.release` field where the OCP release image can be specified.

For a cluster to keep fully operational during an upgrade process, control plane and nodes upgrades need to be orchestrated while satisfying [Kubernetes version skew policy](https://kubernetes.io/releases/version-skew-policy/) at any time. The supported OCP versions are dictated by the running HyperShift Operator.

## Getting Available Upgrade Versions
HyperShift exposes available upgrades in HostedCluster.Status by bubbling up the status of the ClusterVersion resource inside a hosted cluster. This info is purely informational and doesn't determine upgradability, which is dictated by the `.spec.release`. This does result in the loss of some of the builtin features and guardrails from CVO like recommendations, allowed upgrade paths, risks, etc. However, this information is still available in the HostedCluster.Status field for consumers to read. 

The initial HostedCluster custom resource does not have any information in `status.version.availableUpdates` and `status.version.conditionalUpdates`. Set the `spec.channel` to `stable-4.y` where `4.y` is replaced by the release version you specified in `spec.release`. For example, set the `spec.channel` to `stable-4.16` if you set the `spec.release` to `ocp-release:4.16.4-multi`. Once the channel is set, the hypershift operator reconciles it and `status.version` gets populated with the available and conditional updates. It is recommended to use this information to update hosted clusters.

Here is the example of the HostedCluster spec that contains the channel setting.

```
spec:
  autoscaling: {}
  channel: stable-4.16
  clusterID: d6d42268-7dff-4d37-92cf-691bd2d42f41
  configuration: {}
  controllerAvailabilityPolicy: SingleReplica
  dns:
    baseDomain: dev11.red-chesterfield.com
    privateZoneID: Z0180092I0DQRKL55LN0
    publicZoneID: Z00206462VG6ZP0H2QLWK
```

Here is the example of the HostedCluster's `status.version.availableUpdates` that lists all available OCP version upgrades and `status.version.conditionalUpdates` that lists recommended updates associated with known risks.

```
  version:
    availableUpdates:
    - channels:
      - candidate-4.16
      - candidate-4.17
      - eus-4.16
      - fast-4.16
      - stable-4.16
      image: quay.io/openshift-release-dev/ocp-release@sha256:b7517d13514c6308ae16c5fd8108133754eb922cd37403ed27c846c129e67a9a
      url: https://access.redhat.com/errata/RHBA-2024:6401
      version: 4.16.11
    - channels:
      - candidate-4.16
      - candidate-4.17
      - eus-4.16
      - fast-4.16
      - stable-4.16
      image: quay.io/openshift-release-dev/ocp-release@sha256:d08e7c8374142c239a07d7b27d1170eae2b0d9f00ccf074c3f13228a1761c162
      url: https://access.redhat.com/errata/RHSA-2024:6004
      version: 4.16.10
    - channels:
      - candidate-4.16
      - candidate-4.17
      - eus-4.16
      - fast-4.16
      - stable-4.16
      image: quay.io/openshift-release-dev/ocp-release@sha256:6a80ac72a60635a313ae511f0959cc267a21a89c7654f1c15ee16657aafa41a0
      url: https://access.redhat.com/errata/RHBA-2024:5757
      version: 4.16.9
    - channels:
      - candidate-4.16
      - candidate-4.17
      - eus-4.16
      - fast-4.16
      - stable-4.16
      image: quay.io/openshift-release-dev/ocp-release@sha256:ea624ae7d91d3f15094e9e15037244679678bdc89e5a29834b2ddb7e1d9b57e6
      url: https://access.redhat.com/errata/RHSA-2024:5422
      version: 4.16.8
    - channels:
      - candidate-4.16
      - candidate-4.17
      - eus-4.16
      - fast-4.16
      - stable-4.16
      image: quay.io/openshift-release-dev/ocp-release@sha256:e4102eb226130117a0775a83769fe8edb029f0a17b6cbca98a682e3f1225d6b7
      url: https://access.redhat.com/errata/RHSA-2024:4965
      version: 4.16.6
    - channels:
      - candidate-4.16
      - candidate-4.17
      - eus-4.16
      - fast-4.16
      - stable-4.16
      image: quay.io/openshift-release-dev/ocp-release@sha256:f828eda3eaac179e9463ec7b1ed6baeba2cd5bd3f1dd56655796c86260db819b
      url: https://access.redhat.com/errata/RHBA-2024:4855
      version: 4.16.5
    conditionalUpdates:
    - conditions:
      - lastTransitionTime: "2024-09-23T22:33:38Z"
        message: |-
          Could not evaluate exposure to update risk SRIOVFailedToConfigureVF (creating PromQL round-tripper: unable to load specified CA cert /etc/tls/service-ca/service-ca.crt: open /etc/tls/service-ca/service-ca.crt: no such file or directory)
            SRIOVFailedToConfigureVF description: OCP Versions 4.14.34, 4.15.25, 4.16.7 and ALL subsequent versions include kernel datastructure changes which are not compatible with older versions of the SR-IOV operator. Please update SR-IOV operator to versions dated 20240826 or newer before updating OCP.
            SRIOVFailedToConfigureVF URL: https://issues.redhat.com/browse/NHE-1171
        reason: EvaluationFailed
        status: Unknown
        type: Recommended
      release:
        channels:
        - candidate-4.16
        - candidate-4.17
        - eus-4.16
        - fast-4.16
        - stable-4.16
        image: quay.io/openshift-release-dev/ocp-release@sha256:fb321a3f50596b43704dbbed2e51fdefd7a7fd488ee99655d03784d0cd02283f
        url: https://access.redhat.com/errata/RHSA-2024:5107
        version: 4.16.7
      risks:
      - matchingRules:
        - promql:
            promql: |
              group(csv_succeeded{_id="d6d42268-7dff-4d37-92cf-691bd2d42f41", name=~"sriov-network-operator[.].*"})
              or
              0 * group(csv_count{_id="d6d42268-7dff-4d37-92cf-691bd2d42f41"})
          type: PromQL
        message: OCP Versions 4.14.34, 4.15.25, 4.16.7 and ALL subsequent versions
          include kernel datastructure changes which are not compatible with older
          versions of the SR-IOV operator. Please update SR-IOV operator to versions
          dated 20240826 or newer before updating OCP.
        name: SRIOVFailedToConfigureVF
        url: https://issues.redhat.com/browse/NHE-1171
```

## Specifying the OCP release in HostedCluster Custom Resource
`.spec.release` dictates the version of the control plane.

The HostedCluster propagates the intended `.spec.release` to the `HostedControlPlane.spec.release` and runs the appropriate Control Plane Operator version.

The HostedControlPlane orchestrates the rollout of the new version of the Control Plane components along with any OCP component in the data plane through the new version of the [cluster version operator (CVO)](https://github.com/openshift/cluster-version-operator). This includes resources like:

- the CVO itself
- cluster network operator (CNO)
- cluster ingress operator
- manifests for the kube API-server (KAS), scheduler, and manager
- machine approver
- autoscaler
- infra resources needed to enable ingress for control plane endpoints (KAS, ignition, konnectivity, etc.)

Using the information from the `status.version.availableUpdates` and `status.version.conditionalUpdates` described in the previous section, you can set the `.spec.release` in the HostedCluster custom resource to start the control plane upgrade. For example, using the `status.version.conditionalUpdates.conditions.release.image` from the previous section, you can upgrade both the hosted control plane and nodepools by setting the `.spec.release` to `quay.io/openshift-release-dev/ocp-release@sha256:fb321a3f50596b43704dbbed2e51fdefd7a7fd488ee99655d03784d0cd02283f` which is OCP 4.16.7.

## NodePools
`.spec.release` dictates the version of any particular NodePool.

A NodePool will perform a Replace/InPlace rolling upgrade according to `.spec.management.upgradeType`. Replace upgrade will create new instances in the new version while removing old nodes in a rolling fashion. This is usually a good choice in cloud environments where this level of immutability is cost effective. InPlace upgrade will directly perform updates to the Operating System of the existing instances. This is usually a good choice for environments where the infrastructure constraints are higher e.g. bare metal. Using the available upgrades information from the `status.version.availableUpdates` described in the previous section, you can set the `.spec.release` in the NodePool custom resources to start the data plane upgrade.

## Upgrading Hosted Clusters via MCE Console
In the MCE console, select `All clusters` view at the top and navigate to Infrastructure -> Clusters to view managed hosted clusters with the `Upgrade available` link. You can also click on a managed hosted cluster to view the cluster details which also shows the `Upgrade available` link. You can update the control plane and nodepools by clicking the link. The list of the available updates is not from the hosted cluster's CVO and can be different than the available updates from the HostedCluster's `status.version.availableUpdates` and `status.version.conditionalUpdates`. Choosing a wrong release version from the console drop down list that is not recommended in the HostedCluster's `status.version.availableUpdates` and `status.version.conditionalUpdates` could break the hosted cluster. Therefore, referring to the hosted cluster's available and conditional updates in the status is required.

