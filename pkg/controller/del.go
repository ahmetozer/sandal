package controller

import "fmt"

func DeleteContainer(Name string) error {
	for i := range containerList {
		if containerList[i].Name == Name {
			containerList[i] = containerList[len(containerList)-1]
			containerList = containerList[:len(containerList)-1]
			return nil
		}
	}
	return fmt.Errorf("container not found")
}
