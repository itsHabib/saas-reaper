package factory

// Choice describes one supported value shown by the configurator.
type Choice struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Catalog is the public selectable surface of this factory version.
type Catalog struct {
	Languages   []Choice `json:"languages"`
	Databases   []Choice `json:"databases"`
	Deployments []Choice `json:"deployments"`
	Deliveries  []Choice `json:"deliveries"`
}

// ProductCatalog returns the options accepted by Validate.
func ProductCatalog() Catalog {
	return Catalog{
		Languages: []Choice{
			{Value: "go", Label: "Go", Description: "Small compiled service with explicit internal packages"},
			{Value: "typescript", Label: "TypeScript", Description: "Node.js service with strict TypeScript"},
			{Value: "python", Label: "Python", Description: "Python service with strict static checks"},
		},
		Databases: []Choice{
			{Value: "sqlite", Label: "SQLite", Description: "Single-instance embedded authority"},
			{Value: "postgres", Label: "PostgreSQL", Description: "External production authority"},
		},
		Deployments: []Choice{
			{Value: "docker", Label: "Docker Compose", Description: "Local or single-host containers"},
			{Value: "aws-ecs", Label: "AWS ECS/Fargate", Description: "Managed AWS container service with Terraform"},
			{Value: "aws-ec2", Label: "AWS EC2", Description: "Single virtual machine with Terraform and cloud-init"},
			{Value: "gcp-cloud-run", Label: "GCP Cloud Run", Description: "Managed GCP container service with Terraform"},
			{Value: "kubernetes", Label: "Kubernetes", Description: "Portable manifests with Kustomize"},
		},
		Deliveries: []Choice{
			{Value: "directory", Label: "Directory", Description: "Generate an editable repository directory"},
			{Value: "zip", Label: "ZIP", Description: "Generate a portable ZIP archive"},
			{Value: "both", Label: "Both", Description: "Generate the directory and ZIP archive"},
		},
	}
}
