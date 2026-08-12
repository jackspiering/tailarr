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

// OverviewStats summarizes deploy root and docker state.
type OverviewStats struct {
	ManagedCount  int
	OtherCount    int
	RunningNames  []string
	DeployedNames []string
	ManagedHealth map[string]Health
}

// CollectOverview builds a status overview for the deploy path.
func CollectOverview(deployPath string) (OverviewStats, error) {
	var st OverviewStats
	st.ManagedHealth = map[string]Health{}

	deployed, err := scaletail.ListDeployed(deployPath)
	if err != nil {
		return st, err
	}
	for _, s := range deployed {
		st.DeployedNames = append(st.DeployedNames, s.Name)
		if IsManaged(s.Dir) {
			st.ManagedCount++
			st.ManagedHealth[s.Name] = ServiceHealth(s.Name)
		} else {
			st.OtherCount++
		}
	}
	sort.Strings(st.DeployedNames)

	running, _ := RunningServiceNames()
	st.RunningNames = running
	return st, nil
}

// RunningServiceNames lists ScaleTail-style running service names from docker ps.
// Recognizes app-* and tailscale-* container name prefixes, plus compose project labels.
func RunningServiceNames() ([]string, error) {
	if !DockerOK() {
		return nil, nil
	}
	cmd := exec.Command("docker", "ps", "--format", "{{.Names}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := scaleTailServiceFromContainer(line)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
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
	if !DockerOK() {
		return HealthUnknown
	}
	// Do not use --filter name=: Docker treats it as a substring, so "web"
	// would include "web-ui". Match the tailarr.service label or an exact
	// app-/tailscale- container name.
	cmd := exec.Command("docker", "ps", "-a",
		"--format", `{{.Names}}\t{{.State}}\t{{.Status}}\t{{.Label "tailarr.service"}}`)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return HealthStopped
	}
	worst := HealthUnknown
	found := false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		name := parts[0]
		label := ""
		if len(parts) >= 4 {
			label = parts[3]
		}
		if !containerMatchesService(name, label, service) {
			continue
		}
		found = true
		state := strings.ToLower(parts[1])
		status := ""
		if len(parts) >= 3 {
			status = strings.ToLower(parts[2])
		}
		h := classifyHealth(state, status)
		if healthRank(h) > healthRank(worst) || worst == HealthUnknown {
			worst = h
		}
	}
	if !found {
		return HealthStopped
	}
	return worst
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
