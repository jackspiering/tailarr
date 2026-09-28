package deploy

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/jackspiering/tailarr/internal/scaletail"
)

// Health is a coarse container/service health state.
type Health string

const (
	HealthHealthy   Health = "healthy"
	HealthStarting  Health = "starting"
	HealthUnhealthy Health = "unhealthy"
	HealthRunning   Health = "running/no-healthcheck"
	HealthExited    Health = "exited"
	HealthUnknown   Health = "unknown"
	HealthStopped   Health = "stopped"
)

// Container is one Docker container that belongs to a deployed service.
type Container struct {
	Name   string
	State  string
	Status string
	Health Health
}

// ServiceStatus is the live state of one deployed service.
type ServiceStatus struct {
	Name    string
	Managed bool
	// Health is the worst health of the containers, or stopped when the
	// service has none.
	Health     Health
	Containers []Container
}

// OverviewStats summarizes deploy root and docker state.
type OverviewStats struct {
	ManagedCount  int
	OtherCount    int
	RunningNames  []string
	DeployedNames []string
	ManagedHealth map[string]Health
	// Services lists every deployed service with its containers, by name.
	Services []ServiceStatus
	// DockerErr says why docker ps failed. Health is unknown then.
	DockerErr string
}

// CollectOverview builds a status overview for the deploy path from a single
// `docker ps -a` pass.
func CollectOverview(deployPath string) (OverviewStats, error) {
	var st OverviewStats
	st.ManagedHealth = map[string]Health{}

	deployed, err := scaletail.ListDeployed(deployPath)
	if err != nil {
		return st, err
	}
	rows, perr := dockerPS()
	if perr != nil {
		st.DockerErr = perr.Error()
	}
	for _, s := range deployed {
		svc := ServiceStatus{Name: s.Name, Managed: IsManaged(s.Dir), Health: HealthUnknown}
		if perr == nil {
			svc.Containers, svc.Health = serviceContainers(rows, s.Name)
		}
		st.DeployedNames = append(st.DeployedNames, s.Name)
		if svc.Managed {
			st.ManagedCount++
			st.ManagedHealth[s.Name] = svc.Health
		} else {
			st.OtherCount++
		}
		st.Services = append(st.Services, svc)
	}
	sort.Strings(st.DeployedNames)
	st.RunningNames = runningNames(rows)
	return st, nil
}

// psRow is one line of `docker ps -a` output.
type psRow struct {
	name, state, status, label string
}

// dockerPS lists every container once. The tailarr.service label maps a
// container to its service even when the template renames it.
func dockerPS() ([]psRow, error) {
	if !DockerOK() {
		return nil, fmt.Errorf("docker not found in PATH")
	}
	ctx, cancel := probeContext()
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--format", `{{.Names}}\t{{.State}}\t{{.Status}}\t{{.Label "tailarr.service"}}`)
	raw, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("docker ps timed out after %s", probeTimeout)
		}
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	return parsePS(string(raw)), nil
}

func parsePS(raw string) []psRow {
	var rows []psRow
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		r := psRow{name: parts[0], state: strings.ToLower(parts[1])}
		if len(parts) >= 3 {
			r.status = parts[2]
		}
		if len(parts) >= 4 {
			r.label = parts[3]
		}
		rows = append(rows, r)
	}
	return rows
}

// serviceContainers returns the containers of service, sorted by name, and
// their worst health. A service without containers is stopped.
func serviceContainers(rows []psRow, service string) ([]Container, Health) {
	var out []Container
	worst := HealthStopped
	for _, r := range rows {
		if !containerMatchesService(r.name, r.label, service) {
			continue
		}
		h := classifyHealth(r.state, strings.ToLower(r.status))
		if len(out) == 0 || healthRank(h) > healthRank(worst) {
			worst = h
		}
		out = append(out, Container{Name: r.name, State: r.state, Status: r.status, Health: h})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, worst
}

// runningNames lists ScaleTail-style service names of running containers.
func runningNames(rows []psRow) []string {
	seen := map[string]bool{}
	var names []string
	for _, r := range rows {
		if r.state != "running" {
			continue
		}
		name := scaleTailServiceFromContainer(r.name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RunningServiceNames lists ScaleTail-style running service names from docker ps.
// Recognizes app-* and tailscale-* container name prefixes.
func RunningServiceNames() ([]string, error) {
	if !DockerOK() {
		return nil, nil
	}
	rows, err := dockerPS()
	if err != nil {
		return nil, err
	}
	return runningNames(rows), nil
}

func scaleTailServiceFromContainer(container string) string {
	switch {
	case strings.HasPrefix(container, "app-"):
		return strings.TrimPrefix(container, "app-")
	case strings.HasPrefix(container, "tailscale-"):
		return strings.TrimPrefix(container, "tailscale-")
	default:
		return ""
	}
}

// ServiceHealth returns health for a named ScaleTail-style service.
func ServiceHealth(service string) Health {
	return ServiceHealthMap([]string{service})[service]
}

// ServiceHealthMap returns health for each requested service from a single
// `docker ps -a` pass. Services with no matching container are "stopped";
// services whose state cannot be read (for example the daemon is down) are
// "unknown" so failures are not misreported as a stopped stack.
func ServiceHealthMap(services []string) map[string]Health {
	out := make(map[string]Health, len(services))
	// Do not use --filter name=: Docker treats it as a substring, so "web"
	// would include "web-ui". Match the tailarr.service label or an exact
	// app-/tailscale- container name.
	rows, err := dockerPS()
	for _, s := range services {
		out[s] = HealthUnknown
		if err == nil {
			_, out[s] = serviceContainers(rows, s)
		}
	}
	return out
}

// healthFromOutput parses `docker ps -a` output for the requested services.
// Separated from ServiceHealthMap so the parser is testable without Docker.
func healthFromOutput(raw string, services []string) map[string]Health {
	rows := parsePS(raw)
	out := make(map[string]Health, len(services))
	for _, s := range services {
		_, out[s] = serviceContainers(rows, s)
	}
	return out
}

func containerMatchesService(container, label, service string) bool {
	if label != "" && label == service {
		return true
	}
	if container == "app-"+service || container == "tailscale-"+service {
		return true
	}
	return scaleTailServiceFromContainer(container) == service
}

func classifyHealth(state, status string) Health {
	switch state {
	case "running":
		switch {
		case strings.Contains(status, "(healthy)"):
			return HealthHealthy
		case strings.Contains(status, "(health: starting)"), strings.Contains(status, "(starting)"):
			return HealthStarting
		case strings.Contains(status, "(unhealthy)"):
			return HealthUnhealthy
		default:
			return HealthRunning
		}
	case "exited", "dead", "created", "paused", "restarting", "removing":
		return HealthExited
	default:
		return HealthUnknown
	}
}

func healthRank(h Health) int {
	switch h {
	case HealthUnhealthy:
		return 5
	case HealthExited:
		return 4
	case HealthUnknown:
		return 3
	case HealthStarting:
		return 2
	case HealthRunning:
		return 1
	case HealthHealthy:
		return 0
	case HealthStopped:
		return 4
	default:
		return 3
	}
}

// FormatOverview returns a human-readable overview block.
func FormatOverview(st OverviewStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Managed deployments: %d\n", st.ManagedCount)
	fmt.Fprintf(&b, "Other compose dirs:  %d\n", st.OtherCount)
	fmt.Fprintf(&b, "Running (ScaleTail-style names): %d\n", len(st.RunningNames))
	if len(st.DeployedNames) > 0 {
		b.WriteString("\nDeployed:\n")
		for _, name := range st.DeployedNames {
			h := st.ManagedHealth[name]
			if h == "" {
				h = HealthUnknown
			}
			fmt.Fprintf(&b, "  - %s [%s]\n", name, h)
		}
	}
	if len(st.RunningNames) > 0 {
		b.WriteString("\nRunning containers (app-/tailscale- prefix):\n")
		for _, name := range st.RunningNames {
			fmt.Fprintf(&b, "  - %s\n", name)
		}
	}
	return b.String()
}
