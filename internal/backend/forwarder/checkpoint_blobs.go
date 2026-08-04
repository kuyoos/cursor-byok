package forwarder

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
)

const checkpointBlobAckTimeout = 30 * time.Second

type checkpointBlobCacheEntry struct {
	ID             []byte
	Data           []byte
	AcknowledgedAt time.Time
}

func (service *Service) publishCheckpointWithBlobs(requestID string, conversationID string) error {
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", requestID)
	}
	conversation, pendingExecs, pendingInteractions, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		return err
	}
	requestedConversationID := strings.TrimSpace(conversationID)
	actualConversationID := strings.TrimSpace(conversation.ConversationID)
	if requestedConversationID != "" && actualConversationID != "" && requestedConversationID != actualConversationID {
		return fmt.Errorf("checkpoint conversation mismatch: requested=%s actual=%s", requestedConversationID, actualConversationID)
	}
	projection, err := service.projector.ProjectCheckpointProjection(conversation)
	if err != nil {
		return err
	}
	if projection == nil || projection.State == nil {
		return fmt.Errorf("checkpoint projection is empty")
	}
	state := projection.State
	state.PendingToolCalls = buildPendingToolCalls(pendingExecs, pendingInteractions)
	service.rewriteCheckpointTokenDetailsForClient(stream, conversation, state)

	required := make(map[string]struct{}, len(projection.Blobs))
	missing := make([]CheckpointBlob, 0, len(projection.Blobs))
	for _, blob := range projection.Blobs {
		key, err := service.rememberCheckpointBlob(blob)
		if err != nil {
			return err
		}
		required[key] = struct{}{}
		if !service.checkpointBlobAcknowledged(key) {
			missing = append(missing, blob)
		}
	}

	stream.mu.Lock()
	stream.NextCheckpointRevision++
	revision := stream.NextCheckpointRevision
	terminalAction := checkpointTerminalAction{}
	if stream.PendingCheckpoint != nil {
		terminalAction = stream.PendingCheckpoint.TerminalAction
	}
	stream.PendingCheckpoint = &pendingCheckpointPublish{
		Revision:       revision,
		State:          cloneConversationStateStructure(state),
		Required:       required,
		TerminalAction: terminalAction,
	}
	if len(missing) > 0 {
		stream.Phase = TurnPhaseCheckpointing
	}
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()

	if len(missing) == 0 {
		return service.finalizePendingCheckpoint(stream)
	}

	for _, blob := range missing {
		key := string(blob.ID)
		stream.mu.Lock()
		messageID, alreadyPending := stream.PendingCheckpointBlobRequests[key]
		if !alreadyPending {
			stream.NextCheckpointBlobRequestID++
			messageID = stream.NextCheckpointBlobRequestID
			if messageID == 0 {
				stream.NextCheckpointBlobRequestID++
				messageID = stream.NextCheckpointBlobRequestID
			}
			stream.PendingCheckpointBlobRequests[key] = messageID
			stream.PendingCheckpointBlobWrites[messageID] = pendingCheckpointBlobWrite{
				Key:      key,
				Revision: revision,
			}
		}
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		if alreadyPending {
			continue
		}
		if err := service.broker.Publish(requestID, StreamEvent{
			Message: buildSetCheckpointBlobMessage(messageID, blob),
		}); err != nil {
			return err
		}
	}
	service.scheduleStreamTimer(stream, providerTimerKey(streamTimerCheckpointBlobs, ""), checkpointBlobAckTimeout, streamTimerCheckpointBlobs, "", 0, "")
	return nil
}

func cloneConversationStateStructure(state *agentv1.ConversationStateStructure) *agentv1.ConversationStateStructure {
	if state == nil {
		return nil
	}
	cloned, ok := proto.Clone(state).(*agentv1.ConversationStateStructure)
	if !ok || cloned == nil {
		return &agentv1.ConversationStateStructure{}
	}
	return cloned
}

func (service *Service) rememberCheckpointBlob(blob CheckpointBlob) (string, error) {
	if len(blob.ID) == 0 || len(blob.Data) == 0 {
		return "", fmt.Errorf("checkpoint blob must contain id and data")
	}
	digest := sha256.Sum256(blob.Data)
	if !bytes.Equal(digest[:], blob.ID) {
		return "", fmt.Errorf("checkpoint blob id does not match SHA-256 digest")
	}
	key := string(blob.ID)
	service.checkpointBlobMu.Lock()
	defer service.checkpointBlobMu.Unlock()
	if service.checkpointBlobs == nil {
		service.checkpointBlobs = make(map[string]*checkpointBlobCacheEntry)
	}
	entry, exists := service.checkpointBlobs[key]
	if !exists || entry == nil || !bytes.Equal(entry.Data, blob.Data) {
		service.checkpointBlobs[key] = &checkpointBlobCacheEntry{
			ID:   append([]byte(nil), blob.ID...),
			Data: append([]byte(nil), blob.Data...),
		}
	}
	return key, nil
}

func (service *Service) checkpointBlobAcknowledged(key string) bool {
	service.checkpointBlobMu.Lock()
	defer service.checkpointBlobMu.Unlock()
	entry := service.checkpointBlobs[key]
	return entry != nil && !entry.AcknowledgedAt.IsZero()
}

func (service *Service) acknowledgeCheckpointBlob(key string, data []byte) error {
	service.checkpointBlobMu.Lock()
	defer service.checkpointBlobMu.Unlock()
	entry := service.checkpointBlobs[key]
	if entry == nil {
		return fmt.Errorf("unknown checkpoint blob")
	}
	if len(data) > 0 && !bytes.Equal(entry.Data, data) {
		return fmt.Errorf("checkpoint blob data mismatch")
	}
	entry.AcknowledgedAt = time.Now().UTC()
	return nil
}

func setPendingCheckpointTerminalAction(stream *ActiveStream, action checkpointTerminalAction) bool {
	if stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.PendingCheckpoint == nil {
		return false
	}
	stream.PendingCheckpoint.TerminalAction = action
	stream.Phase = TurnPhaseCheckpointing
	stream.UpdatedAt = time.Now().UTC()
	return true
}

func (service *Service) handleCheckpointBlobResult(stream *ActiveStream, message *agentv1.KvClientMessage) error {
	if stream == nil || message == nil {
		return nil
	}
	messageID := message.GetId()
	if messageID == 0 {
		return fmt.Errorf("checkpoint blob result id is required")
	}

	var data []byte
	if result := message.GetSetBlobResult(); result != nil {
		if result.GetError() != nil {
			return fmt.Errorf("client rejected checkpoint blob %d: %s", messageID, strings.TrimSpace(result.GetError().String()))
		}
	} else if result := message.GetGetBlobResult(); result != nil {
		data = append([]byte(nil), result.GetBlobData()...)
		if len(data) == 0 {
			return fmt.Errorf("client returned empty checkpoint blob %d", messageID)
		}
	} else {
		return fmt.Errorf("unsupported checkpoint blob result for id %d", messageID)
	}

	stream.mu.Lock()
	pending, ok := stream.PendingCheckpointBlobWrites[messageID]
	if ok {
		delete(stream.PendingCheckpointBlobWrites, messageID)
		if stream.PendingCheckpointBlobRequests[pending.Key] == messageID {
			delete(stream.PendingCheckpointBlobRequests, pending.Key)
		}
	}
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if !ok {
		// A late acknowledgement from an earlier checkpoint revision is harmless.
		return nil
	}
	if err := service.acknowledgeCheckpointBlob(pending.Key, data); err != nil {
		return err
	}
	return service.finalizePendingCheckpoint(stream)
}

func (service *Service) finalizePendingCheckpoint(stream *ActiveStream) error {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	pending := stream.PendingCheckpoint
	if pending == nil {
		stream.mu.Unlock()
		return nil
	}
	required := make([]string, 0, len(pending.Required))
	for key := range pending.Required {
		required = append(required, key)
	}
	stream.mu.Unlock()
	for _, key := range required {
		if !service.checkpointBlobAcknowledged(key) {
			return nil
		}
	}

	stream.mu.Lock()
	if stream.PendingCheckpoint != pending {
		stream.mu.Unlock()
		return nil
	}
	state := cloneConversationStateStructure(pending.State)
	action := pending.TerminalAction
	stream.PendingCheckpoint = nil
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	clearStreamTimer(stream, providerTimerKey(streamTimerCheckpointBlobs, ""))

	if err := service.broker.Publish(stream.RequestID, StreamEvent{Message: buildCheckpointMessage(state)}); err != nil {
		return err
	}
	return service.finishCheckpointTerminalAction(stream, action)
}

func (service *Service) finishCheckpointTerminalAction(stream *ActiveStream, action checkpointTerminalAction) error {
	if stream == nil {
		return nil
	}
	switch action.kind {
	case checkpointTerminalActionComplete:
		completion := action.completion
		usage := completion.Usage
		if err := service.broker.Publish(stream.RequestID, StreamEvent{
			Message: buildTurnEndedMessage(usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens),
		}); err != nil {
			return err
		}
		if err := service.broker.Complete(stream.RequestID, "", ""); err != nil {
			return err
		}
		service.setTurnPhase(stream, TurnPhaseCompleted)
	case checkpointTerminalActionCancel:
		if err := service.broker.Cancel(stream.RequestID, firstNonEmpty(action.cancelMessage, "[canceled] User aborted request")); err != nil {
			return err
		}
		service.setTurnPhase(stream, TurnPhaseCanceled)
	default:
		return service.reconcileStream(stream)
	}
	return nil
}

func stopActiveProvider(stream *ActiveStream) func() {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	cancel := stream.ProviderCancel
	stream.ProviderCancel = nil
	stream.ProviderActive = false
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	return cancel
}

func (service *Service) handleCheckpointBlobTimeout(stream *ActiveStream) error {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	pending := stream.PendingCheckpoint
	if pending == nil {
		stream.mu.Unlock()
		return nil
	}
	requestID := stream.RequestID
	conversationID := stream.ConversationID
	cancel := stream.ProviderCancel
	stream.PendingCheckpoint = nil
	stream.PendingCheckpointBlobWrites = make(map[uint32]pendingCheckpointBlobWrite)
	stream.PendingCheckpointBlobRequests = make(map[string]uint32)
	stream.ProviderCancel = nil
	stream.ProviderActive = false
	stream.PendingProviderAction = providerActionNone
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	service.setTurnPhase(stream, TurnPhaseFailed)
	message := fmt.Sprintf("checkpoint blob acknowledgement timed out for conversation %s", strings.TrimSpace(conversationID))
	service.debug.LogRuntime(nil, requestID, conversationID, "checkpoint_blob_timeout", map[string]any{
		"revision":       pending.Revision,
		"required_blobs": len(pending.Required),
	})
	return service.broker.Fail(requestID, "checkpoint_blob_timeout", message)
}
