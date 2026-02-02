package resources

import (
	"bufio"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	cgroupBasePath = "/sys/fs/cgroup/sandal"
	cpuPeriod      = 100000 // 100ms in microseconds
)

// ParseMemory parses memory strings like "512M", "1G", "1073741824"
// Returns bytes as int64
func ParseMemory(input string) (int64, error) {
	if input == "" {
		return 0, fmt.Errorf("empty memory limit")
	}

	// Try to parse as raw bytes first
	if val, err := strconv.ParseInt(input, 10, 64); err == nil {
		if val < 0 {
			return 0, fmt.Errorf("memory limit cannot be negative")
		}
		return val, nil
	}

	// Parse with unit suffix
	input = strings.TrimSpace(input)
	var multiplier int64
	var numStr string

	// Check suffix and set multiplier
	if strings.HasSuffix(input, "Ki") {
		multiplier = 1024
		numStr = strings.TrimSuffix(input, "Ki")
	} else if strings.HasSuffix(input, "Mi") {
		multiplier = 1024 * 1024
		numStr = strings.TrimSuffix(input, "Mi")
	} else if strings.HasSuffix(input, "Gi") {
		multiplier = 1024 * 1024 * 1024
		numStr = strings.TrimSuffix(input, "Gi")
	} else if strings.HasSuffix(input, "Ti") {
		multiplier = 1024 * 1024 * 1024 * 1024
		numStr = strings.TrimSuffix(input, "Ti")
	} else if strings.HasSuffix(input, "K") {
		multiplier = 1000
		numStr = strings.TrimSuffix(input, "K")
	} else if strings.HasSuffix(input, "M") {
		multiplier = 1000 * 1000
		numStr = strings.TrimSuffix(input, "M")
	} else if strings.HasSuffix(input, "G") {
		multiplier = 1000 * 1000 * 1000
		numStr = strings.TrimSuffix(input, "G")
	} else if strings.HasSuffix(input, "T") {
		multiplier = 1000 * 1000 * 1000 * 1000
		numStr = strings.TrimSuffix(input, "T")
	} else {
		return 0, fmt.Errorf("invalid memory format: use format like 512M, 1G, or bytes")
	}

	// Parse the numeric part (support decimals)
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory value: %w", err)
	}

	if val < 0 {
		return 0, fmt.Errorf("memory limit cannot be negative")
	}

	result := int64(val * float64(multiplier))
	return result, nil
}

// ParseCPU parses CPU strings like "0.5", "2"
// Returns quota (microseconds) and period (microseconds) for cgroup cpu.max
func ParseCPU(input string) (quota int64, period int64, error error) {
	if input == "" {
		return 0, 0, fmt.Errorf("empty CPU limit")
	}

	val, err := strconv.ParseFloat(input, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid CPU limit: %w", err)
	}

	if val <= 0 {
		return 0, 0, fmt.Errorf("CPU limit must be greater than 0")
	}

	quota = int64(val * float64(cpuPeriod))
	return quota, cpuPeriod, nil
}

// SetupCgroup creates and configures cgroup for container
// Returns cgroup path
func SetupCgroup(containerName, memoryLimit, cpuLimit string) (string, error) {
	// Enable controllers in root cgroup first (to make them available for sandal cgroup)
	if err := enableControllersInRoot(); err != nil {
		return "", fmt.Errorf("failed to enable controllers in root: %w", err)
	}

	// Create base sandal cgroup if not exists
	if err := os.MkdirAll(cgroupBasePath, 0755); err != nil {
		return "", fmt.Errorf("failed to create base cgroup: %w", err)
	}

	// Enable controllers in sandal cgroup (to make them available for container cgroups)
	if err := enableControllersInSandal(); err != nil {
		return "", fmt.Errorf("failed to enable controllers in sandal: %w", err)
	}

	// Create container-specific cgroup
	cgroupPath := filepath.Join(cgroupBasePath, containerName)
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create container cgroup: %w", err)
	}

	// Set memory limit if specified
	if memoryLimit != "" {
		bytes, err := ParseMemory(memoryLimit)
		if err != nil {
			return "", err
		}
		if err := setMemoryLimit(cgroupPath, bytes); err != nil {
			// If memory controller not available, warn but continue
			if strings.Contains(err.Error(), "controller not available") || strings.Contains(err.Error(), "does not exist") {
				slog.Warn("memory controller not available, memory limit not enforced", "limit", memoryLimit)
			} else {
				return "", fmt.Errorf("failed to set memory limit: %w", err)
			}
		} else {
			slog.Debug("memory limit set", "path", cgroupPath, "bytes", bytes)
		}
	}

	// Set CPU limit if specified
	if cpuLimit != "" {
		quota, period, err := ParseCPU(cpuLimit)
		if err != nil {
			return "", err
		}
		if err := setCPULimit(cgroupPath, quota, period); err != nil {
			// If cpu controller not available, warn but continue
			if strings.Contains(err.Error(), "controller not available") || strings.Contains(err.Error(), "does not exist") {
				slog.Warn("cpu controller not available, CPU limit not enforced", "limit", cpuLimit)
			} else {
				return "", fmt.Errorf("failed to set CPU limit: %w", err)
			}
		} else {
			slog.Debug("CPU limit set", "path", cgroupPath, "quota", quota, "period", period)
		}
	}

	return cgroupPath, nil
}

// getAvailableControllers checks which controllers are available
func getAvailableControllers() (hasCPU, hasMemory bool, err error) {
	rootControllers := filepath.Join("/sys/fs/cgroup", "cgroup.controllers")
	data, err := os.ReadFile(rootControllers)
	if err != nil {
		return false, false, fmt.Errorf("failed to read root controllers: %w", err)
	}

	controllers := string(data)
	hasCPU = strings.Contains(controllers, "cpu")
	hasMemory = strings.Contains(controllers, "memory")

	// Log which controllers are available
	if !hasCPU {
		slog.Warn("cpu controller not available in cgroup v2")
	}
	if !hasMemory {
		slog.Warn("memory controller not available in cgroup v2")
	}

	return hasCPU, hasMemory, nil
}

// enableControllersInRoot enables controllers in root cgroup to make them available for sandal cgroup
func enableControllersInRoot() error {
	hasCPU, hasMemory, err := getAvailableControllers()
	if err != nil {
		return err
	}

	// Build list of controllers to enable
	var controllersToEnable []string
	if hasCPU {
		controllersToEnable = append(controllersToEnable, "+cpu")
	}
	if hasMemory {
		controllersToEnable = append(controllersToEnable, "+memory")
	}

	if len(controllersToEnable) == 0 {
		return fmt.Errorf("neither cpu nor memory controller available in cgroup v2")
	}

	// Enable controllers in root cgroup's subtree_control
	subtreeControl := filepath.Join("/sys/fs/cgroup", "cgroup.subtree_control")
	controllerString := strings.Join(controllersToEnable, " ")

	// Try to enable, but don't fail if already enabled
	if err := os.WriteFile(subtreeControl, []byte(controllerString), 0644); err != nil {
		// Check if error is because already enabled or permission denied
		if !os.IsPermission(err) && !strings.Contains(err.Error(), "Invalid argument") {
			return fmt.Errorf("failed to enable controllers in root: %w", err)
		}
	}

	return nil
}

// enableControllersInSandal enables controllers in sandal cgroup to make them available for container cgroups
func enableControllersInSandal() error {
	hasCPU, hasMemory, err := getAvailableControllers()
	if err != nil {
		return err
	}

	// Build list of controllers to enable
	var controllersToEnable []string
	if hasCPU {
		controllersToEnable = append(controllersToEnable, "+cpu")
	}
	if hasMemory {
		controllersToEnable = append(controllersToEnable, "+memory")
	}

	if len(controllersToEnable) == 0 {
		return nil // Already warned in getAvailableControllers
	}

	// Enable controllers in sandal cgroup's subtree_control
	subtreeControl := filepath.Join(cgroupBasePath, "cgroup.subtree_control")
	controllerString := strings.Join(controllersToEnable, " ")

	// Try to enable, but don't fail if already enabled
	if err := os.WriteFile(subtreeControl, []byte(controllerString), 0644); err != nil {
		// Check if error is because already enabled or permission denied
		if !os.IsPermission(err) && !strings.Contains(err.Error(), "Invalid argument") {
			return fmt.Errorf("failed to enable controllers in sandal: %w", err)
		}
	}

	return nil
}

// setMemoryLimit writes memory limit to cgroup
func setMemoryLimit(cgroupPath string, limitBytes int64) error {
	memoryMaxFile := filepath.Join(cgroupPath, "memory.max")

	// Check if memory controller is available (file exists)
	if _, err := os.Stat(memoryMaxFile); os.IsNotExist(err) {
		return fmt.Errorf("memory.max file does not exist (memory controller not available)")
	}

	value := fmt.Sprintf("%d", limitBytes)

	if err := os.WriteFile(memoryMaxFile, []byte(value), 0644); err != nil {
		return fmt.Errorf("failed to write memory.max: %w", err)
	}

	return nil
}

// setCPULimit writes CPU limit to cgroup
func setCPULimit(cgroupPath string, quota, period int64) error {
	cpuMaxFile := filepath.Join(cgroupPath, "cpu.max")

	// Check if cpu controller is available (file exists)
	if _, err := os.Stat(cpuMaxFile); os.IsNotExist(err) {
		return fmt.Errorf("cpu.max file does not exist (cpu controller not available)")
	}

	value := fmt.Sprintf("%d %d", quota, period)

	if err := os.WriteFile(cpuMaxFile, []byte(value), 0644); err != nil {
		return fmt.Errorf("failed to write cpu.max: %w", err)
	}

	return nil
}

// AddProcess moves process to cgroup
func AddProcess(cgroupPath string, pid int) error {
	procsFile := filepath.Join(cgroupPath, "cgroup.procs")
	value := fmt.Sprintf("%d", pid)

	if err := os.WriteFile(procsFile, []byte(value), 0644); err != nil {
		return fmt.Errorf("failed to add process to cgroup: %w", err)
	}

	return nil
}

// RemoveCgroup removes cgroup directory
func RemoveCgroup(cgroupPath string) error {
	err := os.Remove(cgroupPath)
	if err != nil {
		// Ignore if already removed
		if os.IsNotExist(err) {
			return nil
		}
		// Ignore if busy (has processes)
		if pathErr, ok := err.(*os.PathError); ok {
			if pathErr.Err == syscall.EBUSY {
				slog.Debug("cgroup busy, will be cleaned by kernel", "path", cgroupPath)
				return nil
			}
		}
		return fmt.Errorf("failed to remove cgroup: %w", err)
	}

	return nil
}

// GenerateProcFiles creates custom /proc/meminfo and /proc/cpuinfo files
// Stores them in <rootfsDir>/.sandal-proc/
func GenerateProcFiles(rootfsDir, memoryLimit, cpuLimit string) error {
	procDir := filepath.Join(rootfsDir, ".sandal-proc")

	// Create directory
	if err := os.MkdirAll(procDir, 0755); err != nil {
		return fmt.Errorf("failed to create proc directory: %w", err)
	}

	// Generate meminfo if memory limit set
	if memoryLimit != "" {
		bytes, err := ParseMemory(memoryLimit)
		if err != nil {
			return err
		}

		content, err := generateMeminfo(bytes)
		if err != nil {
			return fmt.Errorf("failed to generate meminfo: %w", err)
		}

		meminfoPath := filepath.Join(procDir, "meminfo")
		if err := os.WriteFile(meminfoPath, []byte(content), 0444); err != nil {
			return fmt.Errorf("failed to write meminfo: %w", err)
		}
		slog.Debug("generated custom meminfo", "path", meminfoPath)
	}

	// Generate cpuinfo if CPU limit set
	if cpuLimit != "" {
		numCPUs, err := strconv.ParseFloat(cpuLimit, 64)
		if err != nil {
			return err
		}

		content, err := generateCpuinfo(numCPUs)
		if err != nil {
			return fmt.Errorf("failed to generate cpuinfo: %w", err)
		}

		cpuinfoPath := filepath.Join(procDir, "cpuinfo")
		if err := os.WriteFile(cpuinfoPath, []byte(content), 0444); err != nil {
			return fmt.Errorf("failed to write cpuinfo: %w", err)
		}
		slog.Debug("generated custom cpuinfo", "path", cpuinfoPath)
	}

	return nil
}

// generateMeminfo creates custom /proc/meminfo content
func generateMeminfo(limitBytes int64) (string, error) {
	// Read host /proc/meminfo
	hostMeminfo, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "", fmt.Errorf("failed to read host meminfo: %w", err)
	}

	// Parse host meminfo
	lines := strings.Split(string(hostMeminfo), "\n")
	result := &strings.Builder{}

	var hostMemTotal int64

	// First pass: get host MemTotal
	for _, line := range lines {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				val, _ := strconv.ParseInt(fields[1], 10, 64)
				hostMemTotal = val * 1024 // Convert KB to bytes
				break
			}
		}
	}

	if hostMemTotal == 0 {
		hostMemTotal = limitBytes // Fallback
	}

	// Calculate scaling factor
	scaleFactor := float64(limitBytes) / float64(hostMemTotal)
	limitKB := limitBytes / 1024

	// Second pass: adjust values
	for _, line := range lines {
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			result.WriteString(line + "\n")
			continue
		}

		key := fields[0]

		// Keys that should be scaled to match the limit
		if strings.HasPrefix(key, "MemTotal:") ||
		   strings.HasPrefix(key, "MemFree:") ||
		   strings.HasPrefix(key, "MemAvailable:") ||
		   strings.HasPrefix(key, "Buffers:") ||
		   strings.HasPrefix(key, "Cached:") ||
		   strings.HasPrefix(key, "SwapCached:") ||
		   strings.HasPrefix(key, "Active:") ||
		   strings.HasPrefix(key, "Inactive:") ||
		   strings.HasPrefix(key, "SwapTotal:") ||
		   strings.HasPrefix(key, "SwapFree:") {

			val, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				result.WriteString(line + "\n")
				continue
			}

			// Scale the value
			scaledVal := int64(float64(val) * scaleFactor)

			// Special case for MemTotal - use exact limit
			if strings.HasPrefix(key, "MemTotal:") {
				scaledVal = limitKB
			}

			if len(fields) >= 3 {
				result.WriteString(fmt.Sprintf("%-16s %12d %s\n", key, scaledVal, fields[2]))
			} else {
				result.WriteString(fmt.Sprintf("%-16s %12d\n", key, scaledVal))
			}
		} else {
			result.WriteString(line + "\n")
		}
	}

	return result.String(), nil
}

// generateCpuinfo creates custom /proc/cpuinfo content
func generateCpuinfo(numCPUs float64) (string, error) {
	// Read host /proc/cpuinfo
	hostCpuinfo, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "", fmt.Errorf("failed to read host cpuinfo: %w", err)
	}

	// Round up to nearest integer
	cpuCount := int(math.Ceil(numCPUs))
	if cpuCount < 1 {
		cpuCount = 1
	}

	// Parse host cpuinfo and extract CPU entries
	scanner := bufio.NewScanner(strings.NewReader(string(hostCpuinfo)))
	result := &strings.Builder{}
	currentCPU := 0
	inCPUBlock := false
	cpuBlockLines := []string{}

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "processor") {
			// Save previous CPU block if we have one and need it
			if currentCPU < cpuCount && len(cpuBlockLines) > 0 {
				// Write the CPU block with adjusted processor number
				for _, blockLine := range cpuBlockLines {
					if strings.HasPrefix(blockLine, "processor") {
						result.WriteString(fmt.Sprintf("processor\t: %d\n", currentCPU))
					} else {
						result.WriteString(blockLine + "\n")
					}
				}
				result.WriteString("\n")
				currentCPU++
			}

			// Stop if we've written enough CPUs
			if currentCPU >= cpuCount {
				break
			}

			// Start new CPU block
			inCPUBlock = true
			cpuBlockLines = []string{line}
		} else if line == "" {
			// End of CPU block
			inCPUBlock = false
		} else if inCPUBlock {
			cpuBlockLines = append(cpuBlockLines, line)
		}
	}

	// Write the last CPU block if needed (even if inCPUBlock is false due to trailing empty line)
	if currentCPU < cpuCount && len(cpuBlockLines) > 0 {
		for _, blockLine := range cpuBlockLines {
			if strings.HasPrefix(blockLine, "processor") {
				result.WriteString(fmt.Sprintf("processor\t: %d\n", currentCPU))
			} else {
				result.WriteString(blockLine + "\n")
			}
		}
		result.WriteString("\n")
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to scan cpuinfo: %w", err)
	}

	return result.String(), nil
}

// CleanupProcFiles removes generated proc files
func CleanupProcFiles(rootfsDir string) {
	procDir := filepath.Join(rootfsDir, ".sandal-proc")
	if err := os.RemoveAll(procDir); err != nil {
		slog.Debug("failed to cleanup proc files", "error", err)
	}
}
