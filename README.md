# rollouts-plugin-loki
This contains an example loki plugin for use with Argo Rollouts plugin system. This plugin allows you to use a loki query to check the new version app during the rollout.

### Build

To build a release build run the command below:
```bash
make build-loki-plugin
```

To build a debug build run the command below:
```bash
make build-loki-plugin-debug
```

### Attaching a debugger to debug build
If using goland you can attach a debugger to the debug build by following the directions https://www.jetbrains.com/help/go/attach-to-running-go-processes-with-debugger.html

You can also do this with many other debuggers as well. Including cli debuggers like delve.
## Using a Metric Plugin

There are two methods of installing and using an argo rollouts plugin. The first method is to mount up the plugin executable
into the rollouts controller container. The second method is to use a HTTP(S) server to host the plugin executable.

### Mounting the plugin executable into the rollouts controller container

There are a few ways to mount the plugin executable into the rollouts controller container. Some of these will depend on your
particular infrastructure. Here are a few methods:

* Using an init container to download the plugin executable
* Using a Kubernetes volume mount with a shared volume such as NFS, EBS, etc.
* Building the plugin into the rollouts controller container

Then you can use setup the configmap to point to the plugin executable. Example:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argo-rollouts-config
data:
  plugins: |-
    metricProviderPlugins: >-
    - name: "ManakinCubber/rollouts-plugin-loki"
      location: "file://./my-custom-plugin"
```

### Using a HTTP(S) server to host the plugin executable

Argo Rollouts supports downloading the plugin executable from a HTTP(S) server. To use this method, you will need to
configure the controller via the `argo-rollouts-config` configmaps `pluginLocation` to an http(s) url. Example:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argo-rollouts-config
data:
  plugins: |-
    metricProviderPlugins: >-
    - name: "ManakinCubber/rollouts-plugin-loki"
      location: "https://github.com/ManakinCubber/rollouts-plugin-loki/releases/download/v1.0.0/loki-plugin_1.0.0_linux_arm64"
      sha256: "08f588b1c799a37bbe8d0fc74cc1b1492dd70b2c" #optional sha256 checksum of the plugin executable
```

### Sample Analysis Template

An example for this sample plugin below :
```
apiVersion: argoproj.io/v1alpha1
kind: AnalysisTemplate
metadata:
  name: example-analysis
spec:
  args:
    - name: basicAuthUsername
      valueFrom:
        secretKeyRef:
          key: username
          name: secret-credential-loki
    - name: basicAuthSecret
      valueFrom:
        secretKeyRef:
          key: password
          name: secret-credential-loki
  metrics:
    - count: 3
      failureCondition: result[0] >= 0.10
      failureLimit: 3
      interval: 5m
      name: example-analysis
      provider:
        plugin:
          ManakinCubber/rollouts-plugin-loki:
            address: https://logs.local
            password: "{{ args.basicAuthSecret }}"
            query: >
              sum(rate({cluster="preprod", namespace="app-example"} |=
              `ERROR` [5m]))
            timeout: 40
            username: "{{ args.basicAuthUsername }}"
```
