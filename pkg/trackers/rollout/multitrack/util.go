package multitrack

func appendElemIfNotExist(arr []string, newElem string) []string {
	for _, elem := range arr {
		if elem == newElem {
			return arr
		}
	}
	return append(arr, newElem)
}

func deleteElemFromArray(arr []string, elemToDelete string) []string {
	newArr := []string{}

	for _, elem := range arr {
		if elem != elemToDelete {
			newArr = append(newArr, elem)
		}
	}

	return newArr
}
