set :base_url, "https://www.packer.io/"

activate :hashicorp do |h|
  h.name        = "packer"
  h.version     = "0.12.2"
  h.github_slug = "mitchellh/packer"
end
