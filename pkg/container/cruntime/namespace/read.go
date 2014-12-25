package namespace

// func (Ns *Namespaces) LoadNamespaceIDs(containerPid int) {
// 	if Ns == nil {
// 		log.Fatal("namespaces are not allocated")
// 		return
// 	}
// 	for name, conf := range *Ns {
// 		if conf.IsHost {
// 			continue
// 		}
// 		conf.System = readNamespace(fmt.Sprintf("/proc/%d/ns/%s", containerPid, name))

// 		(*Ns)[name] = conf
// 	}
// }

// func readNamespace(f string) string {
// 	s, err := os.Readlink(f)
// 	if err != nil {
// 		return ""
// 	}
// 	return parseNamespaceInfo(s)
// }

// func parseNamespaceInfo(s string) string {
// 	ns := strings.Split(s, "[")
// 	if ns == nil {
// 		return s
// 	}
// 	if len(ns) == 2 {
// 		return strings.Trim(ns[1], "]")
// 	}
// 	return s
// }
