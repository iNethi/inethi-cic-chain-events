package syncer

import (
	"context"
	"time"

	"github.com/alitto/pond"
	"github.com/celo-org/celo-blockchain/core/types"
	"github.com/celo-org/celo-blockchain/ethclient"
	"github.com/celo-org/celo-blockchain/event"
	"github.com/inethi/inethi-cic-chain-events/internal/pipeline"
	"github.com/zerodha/logf"
)

const (
	jobTimeout         = 15 * time.Second
	resubscribeBackoff = 2 * time.Second
)

type (
	HeadSyncerOpts struct {
		Logg       logf.Logger
		Pipeline   *pipeline.Pipeline
		Pool       *pond.WorkerPool
		Stats      *Stats
		WsEndpoint string
	}

	HeadSyncer struct {
		ethClient *ethclient.Client
		logg      logf.Logger
		pipeline  *pipeline.Pipeline
		pool      *pond.WorkerPool
		stats     *Stats
	}
)

func NewHeadSyncer(o HeadSyncerOpts) (*HeadSyncer, error) {
	ethClient, err := ethclient.Dial(o.WsEndpoint)
	if err != nil {
		return nil, err
	}

	return &HeadSyncer{
		ethClient: ethClient,
		logg:      o.Logg,
		pipeline:  o.Pipeline,
		pool:      o.Pool,
		stats:     o.Stats,
	}, nil
}

// Start creates a websocket subscription and actively receives new blocks until stopped
// or a critical error occurs.
func (hs *HeadSyncer) Start(ctx context.Context) error {
	headerReceiver := make(chan *types.Header, 1)

	sub := event.ResubscribeErr(resubscribeBackoff, func(ctx context.Context, err error) (event.Subscription, error) {
		if err != nil {
			hs.logg.Error("head syncer: resubscribe error", "error", err)
		}

		return hs.ethClient.SubscribeNewHead(ctx, headerReceiver)
	})
	defer sub.Unsubscribe()

	for {
		select {
		case <-ctx.Done():
			hs.logg.Info("head syncer: shutdown signal received")
			return nil
		case header := <-headerReceiver:
			blockNumber := header.Number.Uint64()
			hs.logg.Debug("head syncer: received new block", "block", blockNumber)
			hs.stats.UpdateHeadCursor(blockNumber)
			hs.pool.Submit(func() {
				ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
				defer cancel()

				if err := hs.pipeline.Run(ctx, blockNumber); err != nil {
					hs.logg.Error("head syncer: pipeline run error", "error", err)
				}
			})
		}
	}
}
