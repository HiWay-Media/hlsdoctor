# hlsdoctor as a Nomad periodic batch job on the docker driver: the same job as
# hlsdoctor.nomad.hcl, with the image from GHCR in place of the release binary fetched
# by an artifact block. The list lives in the job spec; a token can come from Vault or a
# Nomad variable through the template — hlsdoctor never prints it.
#
#   nomad job run -var version=0.1.0 deploy/nomad/hlsdoctor-docker.nomad.hcl
#   nomad job status hlsdoctor
#
# Pin the image by digest (ghcr.io/hiway-media/hlsdoctor@sha256:…) before running this in
# production; :main follows the main branch, for trying a change before it is released.

variable "version" {
  type    = string
  default = "0.1.0"
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
        image = "ghcr.io/hiway-media/hlsdoctor:${var.version}"
        args  = ["check", "--from", "/local/streams.txt", "--exit-on", "bad", "--no-ok", "--timeout", "10s"]
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}
