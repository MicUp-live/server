# The bucket is created by ../aws-accounts/micup-production so the application
# project cannot administer its own state or GitHub deployment identity.
terraform {
  backend "s3" {
    bucket       = "micup-live-production-tfstate-223662764170"
    key          = "micup-live/server/production.tfstate"
    region       = "us-east-2"
    encrypt      = true
    use_lockfile = true
  }
}
