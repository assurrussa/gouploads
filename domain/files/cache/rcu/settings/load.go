package rcusettings

import (
	"context"
	"fmt"
)

// LoadData Обновить данные конфигурации.
func LoadData(settingsUseCase settingsUseCase) func(context.Context) (map[string]Data, error) {
	return func(ctx context.Context) (map[string]Data, error) {
		data, err := settingsUseCase.Handle(ctx)
		if err != nil {
			return nil, fmt.Errorf("settingsUseCase.Handle: %w", err)
		}

		return map[string]Data{
			cacheKeyAll: data,
		}, nil
	}
}
