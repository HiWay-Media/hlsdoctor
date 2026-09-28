# hlsdoctor as a Nomad periodic batch job on the docker driver: the same job as
# hlsdoctor.nomad.hcl, with the image from GHCR in place of the release binary fetched
# by an artifact block. The list lives in the job spec; a token can come from Vault or a
# Nomad variable through the template — hlsdoctor never prints it.
#
#   nomad job run deploy/nomad/hlsdoctor-docker.nomad.hcl
#   nomad job run -var image=ghcr.io/hiway-media/hlsdoctor@sha256:… deploy/nomad/hlsdoctor-docker.nomad.hcl
#   nomad job status hlsdoctor
#
# The image is pinned by digest, so every run of the periodic job probes with the same
# binary until someone moves the pin. The default is :main at 1d19443 (2026-09-25),
# reporting v0.0.1-dev+1d19443 — the build for the week on the channels (HLD-9); after a
# release, pin the digest of :<version> instead:
#   docker buildx imagetools inspect ghcr.io/hiway-media/hlsdoctor:0.1.0 --format '{{json .Manifest.Digest}}'

variable "image" {
  type    = string
  default = "ghcr.io/hiway-media/hlsdoctor@sha256:202b7eaae64909dbb24b8d0a55f552355c01a9d39f244eb048b32964ce2e6c00"
}

job "hlsdoctor" {
  type        = "batch"
  datacenters = ["*"]
  namespace   = "default"

  periodic {
    crons            = ["*/5 * * * *"]
    prohibit_overlap = true
  }

  group "probe" {
    task "hlsdoctor" {
      driver = "docker"

      template {
        destination = "local/streams.txt"
        data        = <<-EOT
          # one target per line
          https://cdn.example.com/live/channel-1/master.m3u8
          rtmp://origin.example.com/live
        EOT
      }

      # The image's entrypoint is the binary; the task's local/ is mounted at /local.
      # It runs as nonroot and needs nothing from the host beyond the network.
      config {
        image = var.image
        args  = ["check", "--from", "/local/streams.txt", "--exit-on", "bad", "--no-ok", "--timeout", "10s"]
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}
