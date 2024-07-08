package scanner

import (
	"context"
	"sync"

	"github.com/obukhov/redis-inventory/src/adapter"
	"github.com/obukhov/redis-inventory/src/trie"
	"github.com/rs/zerolog"
)

// RedisServiceInterface abstraction to access redis
type RedisServiceInterface interface {
	ScanKeys(ctx context.Context, options adapter.ScanOptions) <-chan string
	GetKeysCount(ctx context.Context) (int64, error)
	GetMemoryUsage(ctx context.Context, key string) (int64, error)
}

// RedisScanner scans redis keys and puts them in a trie
type RedisScanner struct {
	redisService RedisServiceInterface
	scanProgress adapter.ProgressWriter
	logger       zerolog.Logger
}

// NewScanner creates RedisScanner
func NewScanner(redisService RedisServiceInterface, scanProgress adapter.ProgressWriter, logger zerolog.Logger) *RedisScanner {
	return &RedisScanner{
		redisService: redisService,
		scanProgress: scanProgress,
		logger:       logger,
	}
}

// Scan initiates scanning process
func (s *RedisScanner) Scan(options adapter.ScanOptions, result *trie.Trie) {
	var totalCount int64
	if options.Pattern == "*" || options.Pattern == "" {
		totalCount = s.getKeysCount()
	}

	safeResult := newThreadSafeResult(result)
	var wg sync.WaitGroup

	s.scanProgress.Start(totalCount)
	keys := s.redisService.ScanKeys(context.Background(), options)
	for worker := 0; worker < options.Workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range keys {
				res, err := s.redisService.GetMemoryUsage(context.Background(), key)
				if err != nil {
					s.logger.Error().Err(err).Msgf("Error dumping key %s", key)
					continue
				}

				safeResult.Add(
					key,
					trie.ParamValue{Param: trie.BytesSize, Value: res},
					trie.ParamValue{Param: trie.KeysCount, Value: 1},
				)

				s.logger.Debug().Msgf("Dump %s value: %d", key, res)
				s.scanProgress.Increment()
			}
			s.logger.Info().Msgf("Worker %d done", worker+1)
		}()
	}
	wg.Wait()
	s.scanProgress.Stop()
}

type threadSafeResult struct {
	mutex  sync.Mutex
	result *trie.Trie
}

func newThreadSafeResult(result *trie.Trie) *threadSafeResult {
	return &threadSafeResult{result: result}
}

func (r *threadSafeResult) Add(key string, paramValues ...trie.ParamValue) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.result.Add(key, paramValues...)
}

func (s *RedisScanner) getKeysCount() int64 {
	res, err := s.redisService.GetKeysCount(context.Background())
	if err != nil {
		s.logger.Error().Err(err).Msgf("Error getting number of keys")
		return 0
	}

	return res
}
