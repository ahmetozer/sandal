package namespace

func (source Namespaces) Merge(destination Namespaces) (merge Namespaces) {
	merge = make(Namespaces, len(namespaceList))
	for name, conf := range source {
		merge[name] = conf

		if destination[name].String() == "" {
			continue
		}
		if conf.String() != destination[name].String() {
			merge[name] = destination[name]
		}
	}
	return
}
