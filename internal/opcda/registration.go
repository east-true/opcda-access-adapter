package opcda

type itemRegistration struct {
	ItemID        DAItemID
	ServerHandle  uint32
	CanonicalType DAVarType
	AccessRights  DAAccessRights
	Generation    uint64
}

type registrationCache struct {
	maximum    int
	generation uint64
	items      map[DAItemID]itemRegistration
}

func newRegistrationCache(maximum int, generation uint64) *registrationCache {
	return &registrationCache{
		maximum:    maximum,
		generation: generation,
		items:      make(map[DAItemID]itemRegistration),
	}
}

func (cache *registrationCache) get(itemID DAItemID) (itemRegistration, bool) {
	registration, ok := cache.items[itemID]
	if !ok || registration.Generation != cache.generation {
		return itemRegistration{}, false
	}
	return registration, true
}

func (cache *registrationCache) put(registration itemRegistration) bool {
	if registration.Generation != cache.generation {
		return false
	}
	if _, exists := cache.items[registration.ItemID]; !exists && len(cache.items) >= cache.maximum {
		return false
	}
	cache.items[registration.ItemID] = registration
	return true
}

func (cache *registrationCache) remaining() int {
	remaining := cache.maximum - len(cache.items)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (cache *registrationCache) reset(generation uint64) {
	cache.generation = generation
	cache.items = make(map[DAItemID]itemRegistration)
}
