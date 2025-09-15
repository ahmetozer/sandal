package namespace

func (NS Namespaces) GetNamespaceValue(name string) (namespaceValue string) {
	if NS != nil {
		_, k := NS[name]
		if !k {
			return
		}
	}
	if NS[name].UserValue != nil {
		namespaceValue = *NS[name].UserValue
	}
	if NS[name].SystemValue != "" {
		namespaceValue = NS[name].SystemValue
	}
	return
}
