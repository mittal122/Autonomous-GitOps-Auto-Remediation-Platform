# ---------------------------------------------------------------------------
# Namespaces
# ---------------------------------------------------------------------------
resource "kubernetes_namespace" "monitoring" {
  metadata {
    name = "monitoring"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }

  depends_on = [aws_eks_node_group.default]
}

resource "kubernetes_namespace" "argocd" {
  metadata {
    name = "argocd"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }

  depends_on = [aws_eks_node_group.default]
}

resource "kubernetes_namespace" "autosre" {
  metadata {
    name = "autosre"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }

  depends_on = [aws_eks_node_group.default]
}

# ---------------------------------------------------------------------------
# kube-prometheus-stack (Prometheus + Alertmanager + Grafana)
# ---------------------------------------------------------------------------
resource "helm_release" "kube_prometheus_stack" {
  name       = "kube-prometheus-stack"
  repository = "https://prometheus-community.github.io/helm-charts"
  chart      = "kube-prometheus-stack"
  version    = "58.3.3"
  namespace  = kubernetes_namespace.monitoring.metadata[0].name

  timeout         = 600
  cleanup_on_fail = true

  values = [
    yamlencode({
      alertmanager = {
        enabled = true
        config = {
          global = {
            resolve_timeout = "5m"
          }
          route = {
            receiver = "autosre-webhook"
            group_by = ["alertname", "namespace"]
            group_wait      = "30s"
            group_interval  = "5m"
            repeat_interval = "4h"
            routes = [
              {
                receiver = "autosre-webhook"
                matchers = ["severity =~ 'critical|warning'"]
              }
            ]
          }
          receivers = [
            {
              name = "autosre-webhook"
              webhook_configs = [
                {
                  url               = "http://autosre-agent.autosre.svc.cluster.local:8080/webhook/alertmanager"
                  send_resolved     = true
                  max_alerts        = 50
                }
              ]
            }
          ]
        }
      }
      grafana = {
        enabled         = true
        adminPassword   = "changeme-in-production"
        persistence = {
          enabled      = true
          size         = "5Gi"
          storageClass = "gp2"
        }
      }
      prometheus = {
        prometheusSpec = {
          retention = "15d"
          storageSpec = {
            volumeClaimTemplate = {
              spec = {
                storageClassName = "gp2"
                accessModes      = ["ReadWriteOnce"]
                resources = {
                  requests = { storage = "50Gi" }
                }
              }
            }
          }
        }
      }
    })
  ]

  depends_on = [kubernetes_namespace.monitoring]
}

# ---------------------------------------------------------------------------
# Loki (log aggregation — autosre agent polls this for error signals)
# ---------------------------------------------------------------------------
resource "helm_release" "loki" {
  name       = "loki"
  repository = "https://grafana.github.io/helm-charts"
  chart      = "loki"
  version    = "6.6.2"
  namespace  = kubernetes_namespace.monitoring.metadata[0].name

  timeout         = 300
  cleanup_on_fail = true

  values = [
    yamlencode({
      loki = {
        auth_enabled = false
        commonConfig = {
          replication_factor = 1
        }
        storage = {
          type = "filesystem"
        }
      }
      singleBinary = {
        replicas = 1
        persistence = {
          enabled      = true
          size         = "20Gi"
          storageClass = "gp2"
        }
      }
      monitoring = {
        selfMonitoring = {
          enabled = false
          grafanaAgent = {
            installOperator = false
          }
        }
        lokiCanary = {
          enabled = false
        }
      }
    })
  ]

  depends_on = [kubernetes_namespace.monitoring]
}

# ---------------------------------------------------------------------------
# Promtail (ships pod logs to Loki)
# ---------------------------------------------------------------------------
resource "helm_release" "promtail" {
  name       = "promtail"
  repository = "https://grafana.github.io/helm-charts"
  chart      = "promtail"
  version    = "6.15.5"
  namespace  = kubernetes_namespace.monitoring.metadata[0].name

  timeout         = 180
  cleanup_on_fail = true

  values = [
    yamlencode({
      config = {
        clients = [
          { url = "http://loki:3100/loki/api/v1/push" }
        ]
      }
    })
  ]

  depends_on = [helm_release.loki]
}

# ---------------------------------------------------------------------------
# ArgoCD
# ---------------------------------------------------------------------------
resource "helm_release" "argocd" {
  name       = "argocd"
  repository = "https://argoproj.github.io/argo-helm"
  chart      = "argo-cd"
  version    = "7.3.4"
  namespace  = kubernetes_namespace.argocd.metadata[0].name

  timeout         = 600
  cleanup_on_fail = true

  values = [
    yamlencode({
      server = {
        extraArgs = ["--insecure"]
      }
      configs = {
        params = {
          "server.insecure" = "true"
        }
      }
    })
  ]

  depends_on = [kubernetes_namespace.argocd]
}

# ---------------------------------------------------------------------------
# AutoSRE platform (via the local Helm chart)
# ---------------------------------------------------------------------------
resource "helm_release" "autosre" {
  name      = "autosre"
  chart     = "../charts/autosre"
  namespace = kubernetes_namespace.autosre.metadata[0].name

  timeout         = 300
  cleanup_on_fail = true

  values = [
    yamlencode({
      agent = {
        image        = var.autosre_image != "" ? var.autosre_image : "autosre/agent:latest"
        applyEnabled = false
        serviceAccountAnnotations = {
          "eks.amazonaws.com/role-arn" = aws_iam_role.autosre_agent.arn
        }
        env = {
          LOKI_ADDR       = "http://loki.monitoring.svc.cluster.local:3100"
          DIAGNOSER_ADDR  = "http://autosre-diagnoser.autosre.svc.cluster.local:8001"
          LEARNER_ADDR    = "http://autosre-learner.autosre.svc.cluster.local:8002"
        }
      }
      diagnoser = {
        image = var.diagnoser_image != "" ? var.diagnoser_image : "autosre/diagnoser:latest"
      }
      learner = {
        image = var.learner_image != "" ? var.learner_image : "autosre/learner:latest"
      }
    })
  ]

  depends_on = [
    kubernetes_namespace.autosre,
    helm_release.kube_prometheus_stack,
    helm_release.loki,
    helm_release.argocd,
  ]
}
