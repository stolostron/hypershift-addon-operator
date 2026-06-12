# Updating Hypershift

The ACM is packaged as an OCM Addon and deploy to the service cluster with fleet manager.

ACM includes the multicluster engine operator (MCE) which is the component that really packages the hypershift operator and it's related images. ACM provides the policy functionality, and MCE provides all the cluster lifecycle functionality.

## Ways to Rollout

There are four ways for fleet manager to upgrade the hypershift images.

1. Hypershift image configmap

   On a cluster with ACM 2.7.0/MCE 2.2.0 deployed, you have granular control over the hypershift image set. Using a configmap you can override the hypershift images.

   see: https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/upgrading_hypershift_operator.md
   
2. Create a new ACM OCM Addon version

   We build the ACM/MCE every 12 hours. Not all these builds pass bvt/svt. When they do, they can be promoted to OCM Addon. Currently, fleet manager deploys from our `beta` channel into `integration` and `staging`. The promotion is gated by approvals to MR against managed-tenant-bundles and managed-tenant.

   * Right now, the rate of new ACM OCM Addon is on demand and manually, triggered when there is features that we need to make available to SD. This is basically the standard MT Addon update flow to the two MT repos. We have automation to create the MR for approval, just have to tweak.

   * The rate that each build of ACM/MCE pulls in a newer version of hypershift is also manual right now. Updates are through a MR to update a manifest of image SHA. We can automate this, just have not had the need yet. https://github.com/stolostron/backplane-pipeline/blob/2.2-integration/manifest.json#L243-L250

3. Update to a new MCE bundle version

   It is possible to update the MCE bundle only and produce a new ACM OCM Addon that only changes the MCE component, and repeat the steps in 2 above. Currently we don't do this now.

4. Upgrade to a new ACM/MCE via subscription/csv hack

   Because our ACM/MCE operator uses the 3 number semver version, nightly builds don't automatically upgrade from nightly to nightly. However, it is possible to delete the csv/subscription and reapply the subscription on an existing ACM/MCE, and experience a rolling upgrade of ACM/MCE.

   In this way, you can deploy the ACM OCM Addon at an initial level, and then roll update to our downstream snapshot builds.

   NOTE: This requires you to have access to the OCM addon and be able to manipulate the corresponding ACM/MCE subscription and csv. We perform rolling updates of ACM/MCE in our non OCM Addon environments. We have not tried within the context of OCM Addon yet.
