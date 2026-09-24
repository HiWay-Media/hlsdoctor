# hlsdoctor as a Nomad periodic batch job: every five minutes, probe the streams listed
# in a template and exit non-zero when one is BAD, so the allocation's status is the
# alarm. The list lives in the job spec; a token can come from Vault or a Nomad variable
# through the template — hlsdoctor never prints it.
#
#   nomad job run -var version=0.1.0 deploy/nomad/hlsdoctor.nomad.hcl
#   nomad job status hlsdoctor
#
# Pin the version and the checksum to a release before running this in production.

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
      driver = "raw_exec"

      artifact {
        source      = "https://github.com/hiway-media/hlsdoctor/releases/download/v${var.version}/hlsdoctor-v${var.version}-linux-amd64"
        destination = "local/hlsdoctor"
        mode        = "file"
        # options { checksum = "sha256:…" }  ← from the release's checksums file
      }

      template {
        destination = "local/streams.txt"
        data        = <<-EOT
          # one target per line
          https://cdn.example.com/live/channel-1/master.m3u8
          rtmp://origin.example.com/live
        EOT
      }

      config {
        command = "local/hlsdoctor"
        args    = ["check", "--from", "local/streams.txt", "--exit-on", "bad", "--no-ok", "--timeout", "10s"]
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}
