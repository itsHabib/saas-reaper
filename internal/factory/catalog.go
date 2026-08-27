package factory

// CatalogSchema versions the machine-readable configurator contract.
const CatalogSchema = "reaper.dev/catalog/v1"

// Choice describes one supported value shown by the configurator.
type Choice struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ReplicaPolicy describes the configurator defaults and hard replica ceiling.
type ReplicaPolicy struct {
	Default int `json:"default"`
	Maximum int `json:"maximum,omitempty"`
}

// Compatibility is the machine-readable selection policy used by every UI.
type Compatibility struct {
	DeploymentsByDatabase map[string][]string      `json:"deploymentsByDatabase"`
	ReplicasByDeployment  map[string]ReplicaPolicy `json:"replicasByDeployment"`
}

// Catalog is the public selectable surface of this factory version.
type Catalog struct {
	Schema        string        `json:"schema"`
	Languages     []Choice      `json:"languages"`
	Databases     []Choice      `json:"databases"`
	Deployments   []Choice      `json:"deployments"`
	Deliveries    []Choice      `json:"deliveries"`
	Compatibility Compatibility `json:"compatibility"`
}

type pack struct {
	choice  Choice
	version string
}

type databasePack struct {
	pack
	shared bool
}

type deploymentPack struct {
	pack
	requiresShared bool
	databases      []string
	replicas       ReplicaPolicy
}

type deliveryPublisher func(string, string, string) (Result, error)

type deliveryArtifacts struct {
	directory     bool
	archiveSuffix string
}

type deliveryPack struct {
	pack
	artifacts deliveryArtifacts
	publish   deliveryPublisher
}

var languagePacks = []pack{
	newPack("go", "Go", "Small compiled service with explicit internal packages", "v4"),
	newPack("typescript", "TypeScript", "Node.js service with strict TypeScript", "v4"),
	newPack("python", "Python", "Python service with explicit policy boundaries", "v4"),
}

var databasePacks = []databasePack{
	{
		pack:   newPack("sqlite", "SQLite", "Single-instance embedded authority", "v3"),
		shared: false,
	},
	{
		pack:   newPack("postgres", "PostgreSQL", "External production authority", "v3"),
		shared: true,
	},
}

var deploymentPacks = []deploymentPack{
	{
		pack:      newPack("docker", "Docker Compose", "Local or single-host containers", "v2"),
		databases: []string{"sqlite", "postgres"},
		replicas:  ReplicaPolicy{Default: 1, Maximum: 1},
	},
	{
		pack:           newPack("aws-ecs", "AWS ECS/Fargate", "Managed AWS container service with Terraform", "v2"),
		requiresShared: true,
		databases:      []string{"postgres"},
		replicas:       ReplicaPolicy{Default: 2},
	},
	{
		pack:      newPack("aws-ec2", "AWS EC2", "Single virtual machine with Terraform and cloud-init", "v2"),
		databases: []string{"sqlite", "postgres"},
		replicas:  ReplicaPolicy{Default: 1, Maximum: 1},
	},
	{
		pack:           newPack("gcp-cloud-run", "GCP Cloud Run", "Managed GCP container service with Terraform", "v1"),
		requiresShared: true,
		databases:      []string{"postgres"},
		replicas:       ReplicaPolicy{Default: 2},
	},
	{
		pack:           newPack("kubernetes", "Kubernetes", "Portable manifests with Kustomize", "v1"),
		requiresShared: true,
		databases:      []string{"postgres"},
		replicas:       ReplicaPolicy{Default: 2},
	},
}

var deliveryPacks = []deliveryPack{
	{
		pack:      newPack("directory", "Directory", "Generate an editable repository directory", "v1"),
		artifacts: deliveryArtifacts{directory: true},
		publish:   publishDirectory,
	},
	{
		pack:      newPack("zip", "ZIP", "Generate a portable ZIP archive", "v1"),
		artifacts: deliveryArtifacts{archiveSuffix: ".zip"},
		publish:   publishZIP,
	},
	{
		pack:      newPack("both", "Both", "Generate the directory and ZIP archive", "v1"),
		artifacts: deliveryArtifacts{directory: true, archiveSuffix: ".zip"},
		publish:   publishDirectoryAndZIP,
	},
}

func newPack(value, label, description, version string) pack {
	return pack{
		choice:  Choice{Value: value, Label: label, Description: description},
		version: version,
	}
}

// ProductCatalog returns the choices and compatibility policy accepted by Validate.
func ProductCatalog() Catalog {
	return Catalog{
		Schema:        CatalogSchema,
		Languages:     choices(languagePacks),
		Databases:     databaseChoices(),
		Deployments:   deploymentChoices(),
		Deliveries:    deliveryChoices(),
		Compatibility: compatibility(),
	}
}

// CompatibleDeployments returns the deployments accepted for one database.
func CompatibleDeployments(database string) []Choice {
	if _, exists := findDatabase(database); !exists {
		return nil
	}
	compatible := make([]Choice, 0, len(deploymentPacks))
	for _, deployment := range deploymentPacks {
		if !deployment.supportsDatabase(database) {
			continue
		}
		compatible = append(compatible, deployment.choice)
	}
	return compatible
}

func (deployment deploymentPack) supportsDatabase(database string) bool {
	for _, supported := range deployment.databases {
		if supported == database {
			return true
		}
	}
	return false
}

// DefaultReplicas returns the cataloged default for one deployment.
func DefaultReplicas(deployment string) int {
	selected, exists := findDeployment(deployment)
	if !exists {
		return 1
	}
	return selected.replicas.Default
}

func choices(packs []pack) []Choice {
	selected := make([]Choice, 0, len(packs))
	for _, registered := range packs {
		selected = append(selected, registered.choice)
	}
	return selected
}

func databaseChoices() []Choice {
	selected := make([]Choice, 0, len(databasePacks))
	for _, registered := range databasePacks {
		selected = append(selected, registered.choice)
	}
	return selected
}

func deploymentChoices() []Choice {
	selected := make([]Choice, 0, len(deploymentPacks))
	for _, registered := range deploymentPacks {
		selected = append(selected, registered.choice)
	}
	return selected
}

func deliveryChoices() []Choice {
	selected := make([]Choice, 0, len(deliveryPacks))
	for _, registered := range deliveryPacks {
		selected = append(selected, registered.choice)
	}
	return selected
}

func compatibility() Compatibility {
	deployments := make(map[string][]string, len(databasePacks))
	for _, database := range databasePacks {
		compatible := CompatibleDeployments(database.choice.Value)
		deployments[database.choice.Value] = choiceValuesForCatalog(compatible)
	}
	replicas := make(map[string]ReplicaPolicy, len(deploymentPacks))
	for _, deployment := range deploymentPacks {
		replicas[deployment.choice.Value] = deployment.replicas
	}
	return Compatibility{
		DeploymentsByDatabase: deployments,
		ReplicasByDeployment:  replicas,
	}
}

func choiceValuesForCatalog(choices []Choice) []string {
	values := make([]string, 0, len(choices))
	for _, choice := range choices {
		values = append(values, choice.Value)
	}
	return values
}

func findLanguage(value string) (pack, bool) {
	for _, registered := range languagePacks {
		if registered.choice.Value == value {
			return registered, true
		}
	}
	return pack{}, false
}

func findDatabase(value string) (databasePack, bool) {
	for _, registered := range databasePacks {
		if registered.choice.Value == value {
			return registered, true
		}
	}
	return databasePack{}, false
}

func findDeployment(value string) (deploymentPack, bool) {
	for _, registered := range deploymentPacks {
		if registered.choice.Value == value {
			return registered, true
		}
	}
	return deploymentPack{}, false
}

func findDelivery(value string) (deliveryPack, bool) {
	for _, registered := range deliveryPacks {
		if registered.choice.Value == value {
			return registered, true
		}
	}
	return deliveryPack{}, false
}
