package natgateway

import (
	"sync"

	"github.com/onmetal/onmetal-api-net/onmetal-api-net/utils"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NetworkSelector interface {
	MatchesNetwork(name string) bool
}

type networkSelector string

func (sel networkSelector) MatchesNetwork(name string) bool {
	return name == string(sel)
}

func NoVNISelector() NetworkSelector {
	return networkSelector("")
}

func SelectNetwork(name string) NetworkSelector {
	return networkSelector(name)
}

type AutoscalerSelectors struct {
	mu sync.RWMutex

	selectorByKey       *utils.IndexingMap[client.ObjectKey, NetworkSelector]
	keysBySelectorIndex utils.ReverseMapIndex[client.ObjectKey, NetworkSelector]
}

func NewAutoscalerSelectors() *AutoscalerSelectors {
	var (
		selectorByKey       utils.IndexingMap[client.ObjectKey, NetworkSelector]
		keysBySelectorIndex = make(utils.ReverseMapIndex[client.ObjectKey, NetworkSelector])
	)
	selectorByKey.AddIndex(keysBySelectorIndex)

	return &AutoscalerSelectors{
		selectorByKey:       &selectorByKey,
		keysBySelectorIndex: keysBySelectorIndex,
	}
}

func (m *AutoscalerSelectors) Put(key client.ObjectKey, sel NetworkSelector) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.selectorByKey.Put(key, sel)
}

func (m *AutoscalerSelectors) PutIfNotPresent(key client.ObjectKey, sel NetworkSelector) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.selectorByKey.Get(key); !ok {
		m.selectorByKey.Put(key, sel)
	}
}

func (m *AutoscalerSelectors) ReverseSelect(name string) sets.Set[client.ObjectKey] {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.keysBySelectorIndex.Get(SelectNetwork(name))
}

func (m *AutoscalerSelectors) Delete(key client.ObjectKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.selectorByKey.Delete(key)
}
