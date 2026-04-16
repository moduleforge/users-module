# Atlas configuration for users-module/model.
#
# Environments:
#   local — developer workstation (reads DATABASE_URL from env)
#   ci    — CI pipeline (reads DATABASE_URL from env, no dev shadow)
#   dev   — used as Atlas shadow DB for diff/lint (ephemeral)

env "local" {
  src = "file://migrations"
  url = getenv("DATABASE_URL")
  dev = "docker://postgres/16/dev?search_path=public"
  migration {
    dir = "file://migrations"
  }
}

env "ci" {
  src = "file://migrations"
  url = getenv("DATABASE_URL")
  migration {
    dir = "file://migrations"
  }
}
