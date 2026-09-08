env "local" {
  src = "ent://ent/schema"
  url = getenv("DATABASE_URL")
  dev = getenv("DEV_DATABASE_URL")
  migration {
    dir = "file://migrations"
  }
}
