# OCP Guardian Operator

OpenShift Operator zur Cluster-Überwachung mit drei unabhängigen Controllern für RBAC-Drift-Erkennung, Zertifikats-Ablaufüberwachung und Cluster-Degradierungs-Korrelation.

## Controller

### RBAC Drift Monitor (`rdm`)

Überwacht ClusterRoles, ClusterRoleBindings, Roles und RoleBindings gegen eine definierte Baseline. Erkennt unautorisierte Änderungen (Modified) und gelöschte Ressourcen (Deleted).

```
$ oc get rdm
NAME                  DRIFT COUNT   LAST CHECK             AGE
rbac-drift-monitor    2             2024-01-15T10:30:00Z   5d
```

### Certificate Expiry Sentinel (`cem`)

Scannt TLS-Secrets cluster-weit und warnt bei ablaufenden oder abgelaufenen Zertifikaten. Konfigurierbare Warning- und Critical-Schwellwerte.

```
$ oc get cem
NAME                        EXPIRING   EXPIRED   TOTAL SCANNED   LAST SCAN
certificate-expiry-monitor  3          0         142             2024-01-15T10:30:00Z
```

### Cluster Degradation Correlator (`cdm`)

Beobachtet OpenShift ClusterOperators und korreliert deren Available/Degraded/Progressing-Conditions zu einem einheitlichen Cluster-Health-Status.

```
$ oc get cdm
NAME                          HEALTH    DEGRADED   UNAVAILABLE   LAST CHECK
cluster-degradation-monitor   Healthy   0          0             2024-01-15T10:30:00Z
```

## Voraussetzungen

- OpenShift Container Platform 4.x
- Cluster-Admin-Rechte (für CRDs und ClusterRole)
- Zugang zur internen Image-Registry

## Installation

### 1. Image bauen und pushen

```bash
# Registry-Login (Token von der VM holen: oc create token <sa> -n openshift-image-registry)
podman login default-route-openshift-image-registry.apps.<cluster>.<domain> \
  -u <serviceaccount> -p <token> --tls-verify=false

# Image bauen und pushen
make docker-build docker-push \
  IMG=default-route-openshift-image-registry.apps.<cluster>.<domain>/ocp-guardian/ocp-guardian-operator:0.0.1
```

### 2. Operator deployen (OpenShift Templates)

```bash
# Auf dem Cluster: Projekt anlegen
oc new-project ocp-guardian

# Operator Template verarbeiten und anwenden
oc process -f templates/ocp-guardian-operator.yaml \
  -p NAMESPACE=ocp-guardian \
  -p IMAGE=image-registry.openshift-image-registry.svc:5000/ocp-guardian/ocp-guardian-operator:0.0.1 \
  | oc apply -f -

# Prüfen ob der Pod läuft
oc get pods -n ocp-guardian
```

### 3. Monitore aktivieren

Jeder Monitor wird unabhängig über ein eigenes Template deployed:

```bash
# Certificate Expiry Monitor
oc process -f templates/certificate-expiry-monitor.yaml \
  -p WARNING_DAYS=30 \
  -p CRITICAL_DAYS=7 \
  -p CHECK_INTERVAL=60 \
  | oc apply -f -

# Cluster Degradation Monitor
oc process -f templates/cluster-degradation-monitor.yaml \
  -p CHECK_INTERVAL=2 \
  -p DEGRADED_THRESHOLD=3 \
  | oc apply -f -

# RBAC Drift Monitor
oc process -f templates/rbac-drift-monitor.yaml \
  -p SEVERITY=Warning \
  -p BASELINE_CLUSTERROLE=cluster-admin \
  | oc apply -f -
```

### Templates im Cluster-Katalog registrieren (optional)

```bash
oc apply -f templates/ -n openshift
```

Danach sind die Templates in der OCP Web Console unter **Developer Catalog** verfügbar.

## Template-Parameter

### ocp-guardian-operator.yaml

| Parameter | Default | Beschreibung |
|-----------|---------|--------------|
| `NAMESPACE` | `ocp-guardian` | Operator-Namespace |
| `IMAGE` | `image-registry...svc:5000/ocp-guardian/ocp-guardian-operator:0.0.1` | Operator-Image |
| `REPLICAS` | `1` | Anzahl Replicas |
| `CPU_REQUEST` | `10m` | CPU Request |
| `MEMORY_REQUEST` | `128Mi` | Memory Request |
| `CPU_LIMIT` | `500m` | CPU Limit |
| `MEMORY_LIMIT` | `256Mi` | Memory Limit |

### certificate-expiry-monitor.yaml

| Parameter | Default | Beschreibung |
|-----------|---------|--------------|
| `MONITOR_NAME` | `certificate-expiry-monitor` | CR-Name |
| `WARNING_DAYS` | `30` | Tage vor Ablauf für Warning |
| `CRITICAL_DAYS` | `7` | Tage vor Ablauf für Critical |
| `CHECK_INTERVAL` | `60` | Scan-Intervall in Minuten |
| `EXCLUDE_NAMESPACES` | _(leer)_ | Namespaces ausschliessen |

### cluster-degradation-monitor.yaml

| Parameter | Default | Beschreibung |
|-----------|---------|--------------|
| `MONITOR_NAME` | `cluster-degradation-monitor` | CR-Name |
| `CHECK_INTERVAL` | `2` | Prüfintervall in Minuten |
| `DEGRADED_THRESHOLD` | `3` | Anzahl degradierter Operators für Critical |
| `OPERATOR_FILTER` | _(leer)_ | Nur bestimmte ClusterOperators überwachen |

### rbac-drift-monitor.yaml

| Parameter | Default | Beschreibung |
|-----------|---------|--------------|
| `MONITOR_NAME` | `rbac-drift-monitor` | CR-Name |
| `CHECK_INTERVAL` | `5` | Prüfintervall in Minuten |
| `SEVERITY` | `Warning` | Event-Severity (Warning/Critical) |
| `BASELINE_CLUSTERROLE` | `cluster-admin` | ClusterRole als Baseline |

## Status prüfen

```bash
# Alle Monitore auf einen Blick
oc get cem,cdm,rdm

# Details anzeigen
oc describe cem certificate-expiry-monitor
oc describe cdm cluster-degradation-monitor
oc describe rdm rbac-drift-monitor

# Events anzeigen
oc get events --field-selector involvedObject.kind=CertificateExpiryMonitor
oc get events --field-selector involvedObject.kind=ClusterDegradationMonitor
oc get events --field-selector involvedObject.kind=RBACDriftMonitor

# Operator-Logs
oc logs -f deployment/ocp-guardian-controller-manager -n ocp-guardian
```

## Monitoring pausieren

Jeder Monitor kann über das `suspend`-Feld pausiert werden, ohne den CR zu löschen:

```bash
oc patch cem certificate-expiry-monitor --type=merge -p '{"spec":{"suspend":true}}'
```

## Deinstallation

```bash
# Monitore entfernen
oc delete cem,cdm,rdm --all

# Operator entfernen
oc process -f templates/ocp-guardian-operator.yaml -p NAMESPACE=ocp-guardian | oc delete -f -

# Projekt löschen
oc delete project ocp-guardian
```

## Projektstruktur

```
ocp-kubeadmin-monitor/
├── main.go                          # Operator-Entrypoint
├── api/v1alpha1/                    # CRD-Typen (Spec, Status, DeepCopy)
├── internal/controller/             # 3 Reconciler
├── config/                          # Kustomize-Manifeste
│   ├── crd/                         # CRD-Definitionen
│   ├── rbac/                        # RBAC-Manifeste
│   ├── manager/                     # Deployment
│   ├── samples/                     # Beispiel-CRs
│   └── prometheus/                  # ServiceMonitor
├── templates/                       # OpenShift Templates
│   ├── ocp-guardian-operator.yaml
│   ├── rbac-drift-monitor.yaml
│   ├── certificate-expiry-monitor.yaml
│   └── cluster-degradation-monitor.yaml
├── Dockerfile                       # Multi-stage Build (distroless)
├── Makefile                         # Build-Targets
└── PROJECT                          # Operator-SDK Metadata
```

## Tech-Stack

- Go 1.25 / controller-runtime v0.23
- Operator SDK / kubebuilder v4 Layout
- OpenShift API (`config.openshift.io/v1`) für ClusterOperator-Zugriff
- Standard `metav1.Condition` für kubectl-Kompatibilität
- Multi-stage Dockerfile mit distroless Base-Image
