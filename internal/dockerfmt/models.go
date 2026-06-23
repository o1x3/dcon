package dockerfmt

// These structs mirror the JSON emitted by `container ... --format json`.
// Only the fields dcon needs for Docker-style rendering are declared; unknown
// fields are ignored by encoding/json.

// Container matches `container ls --format json` / `container inspect`.
type Container struct {
	ID            string                 `json:"id"`
	Configuration ContainerConfiguration `json:"configuration"`
	Status        ContainerStatus        `json:"status"`
}

type ContainerConfiguration struct {
	ID           string            `json:"id"`
	Image        ImageDescription  `json:"image"`
	Mounts       []Filesystem      `json:"mounts"`
	Ports        []PublishPort     `json:"publishedPorts"`
	Labels       map[string]string `json:"labels"`
	Networks     []AttachmentConf  `json:"networks"`
	InitProcess  ProcessConfig     `json:"initProcess"`
	Platform     Platform          `json:"platform"`
	Resources    Resources         `json:"resources"`
	Rosetta      bool              `json:"rosetta"`
	CreationDate string            `json:"creationDate"`
}

type ImageDescription struct {
	Reference  string     `json:"reference"`
	Descriptor Descriptor `json:"descriptor"`
}

type Descriptor struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
}

type Filesystem struct {
	Source      string   `json:"source"`
	Destination string   `json:"destination"`
	Options     []string `json:"options"`
}

type PublishPort struct {
	HostAddress   string `json:"hostAddress"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Proto         string `json:"proto"`
	Count         int    `json:"count"`
}

type AttachmentConf struct {
	Network string `json:"network"`
}

type ProcessConfig struct {
	Executable       string   `json:"executable"`
	Arguments        []string `json:"arguments"`
	Environment      []string `json:"environment"`
	WorkingDirectory string   `json:"workingDirectory"`
	Terminal         bool     `json:"terminal"`
}

type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant"`
}

type Resources struct {
	CPUs          int    `json:"cpus"`
	MemoryInBytes uint64 `json:"memoryInBytes"`
}

type ContainerStatus struct {
	State       string       `json:"state"`
	Networks    []Attachment `json:"networks"`
	StartedDate string       `json:"startedDate"`
}

type Attachment struct {
	Network     string `json:"network"`
	Hostname    string `json:"hostname"`
	IPv4Address string `json:"ipv4Address"`
	IPv4Gateway string `json:"ipv4Gateway"`
}

// Image matches `container image list --format json`.
type Image struct {
	ID            string             `json:"id"`
	Configuration ImageConfiguration `json:"configuration"`
	Variants      []ImageVariant     `json:"variants"`
}

type ImageConfiguration struct {
	CreationDate string     `json:"creationDate"`
	Name         string     `json:"name"`
	Descriptor   Descriptor `json:"descriptor"`
}

type ImageVariant struct {
	Platform Platform `json:"platform"`
	Digest   string   `json:"digest"`
	Size     int64    `json:"size"`
}

// Volume matches `container volume list --format json`.
type Volume struct {
	ID            string              `json:"id"`
	Configuration VolumeConfiguration `json:"configuration"`
}

type VolumeConfiguration struct {
	Name         string            `json:"name"`
	Driver       string            `json:"driver"`
	Format       string            `json:"format"`
	Source       string            `json:"source"`
	CreationDate string            `json:"creationDate"`
	Labels       map[string]string `json:"labels"`
	Options      map[string]string `json:"options"`
	SizeInBytes  uint64            `json:"sizeInBytes"`
}

// Network matches `container network list --format json`.
type Network struct {
	ID            string               `json:"id"`
	Configuration NetworkConfiguration `json:"configuration"`
	Status        NetworkStatus        `json:"status"`
}

type NetworkConfiguration struct {
	Name         string            `json:"name"`
	Mode         string            `json:"mode"`
	CreationDate string            `json:"creationDate"`
	IPv4Subnet   string            `json:"ipv4Subnet"`
	Labels       map[string]string `json:"labels"`
	Plugin       string            `json:"plugin"`
}

type NetworkStatus struct {
	IPv4Subnet  string `json:"ipv4Subnet"`
	IPv4Gateway string `json:"ipv4Gateway"`
}

// Stats matches `container stats --format json`.
type Stats struct {
	ID               string `json:"id"`
	MemoryUsageBytes uint64 `json:"memoryUsageBytes"`
	MemoryLimitBytes uint64 `json:"memoryLimitBytes"`
	CPUUsageUsec     uint64 `json:"cpuUsageUsec"`
	NetworkRxBytes   uint64 `json:"networkRxBytes"`
	NetworkTxBytes   uint64 `json:"networkTxBytes"`
	BlockReadBytes   uint64 `json:"blockReadBytes"`
	BlockWriteBytes  uint64 `json:"blockWriteBytes"`
	NumProcesses     uint64 `json:"numProcesses"`
}
