// broker.go 负责 request 维度活动流的订阅、广播、取消和终态收口。
package forwarder

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

const subscriberSignalBufferSize = 1
const orphanSubscriberGracePeriod = 30 * time.Second
const terminalStreamRetentionPeriod = 30 * time.Second

type StreamBroker struct {
	mu      sync.RWMutex
	streams map[string]*ActiveStream
	nextID  atomic.Uint64
}

// NewStreamBroker 创建活动流注册表。
func NewStreamBroker() *StreamBroker {
	return &StreamBroker{
		streams: make(map[string]*ActiveStream),
	}
}

// OpenStream 打开活动流；同一 request 的终态流只作为重试占位，不能复用已退出的 actor。
func (broker *StreamBroker) OpenStream(requestID string, conversationID string, turnSeq int64, modelID string, modelName string, mode agentv1.AgentMode, latestUserText string) (*ActiveStream, error) {
	normalizedRequestID := strings.TrimSpace(requestID)
	if normalizedRequestID == "" {
		return nil, nil
	}
	normalizedMode, err := validateSupportedActiveMode(mode)
	if err != nil {
		return nil, err
	}

	for {
		broker.mu.Lock()
		existing, ok := broker.streams[normalizedRequestID]
		if !ok || existing == nil {
			stream := newActiveStream(normalizedRequestID, conversationID, turnSeq, modelID, modelName, normalizedMode, latestUserText)
			broker.streams[normalizedRequestID] = stream
			broker.mu.Unlock()
			return stream, nil
		}

		existing.mu.Lock()
		terminal := isTerminalStreamStatus(existing.Status) || existing.Phase == TurnPhaseCanceled || existing.Phase == TurnPhaseCompleted || existing.Phase == TurnPhaseFailed
		if !terminal {
			updateActiveStreamContextLocked(existing, conversationID, turnSeq, modelID, modelName, normalizedMode, latestUserText)
			existing.mu.Unlock()
			broker.mu.Unlock()
			return existing, nil
		}
		actorDone := existing.ActorDone
		existing.mu.Unlock()
		broker.mu.Unlock()

		// 终态命令通常已在这里之前退出 actor；等待它收口，避免重置后旧 actor
		// 继续使用同一 request_id 写入新流。
		if actorDone != nil {
			<-actorDone
		}

		broker.mu.Lock()
		current, stillPresent := broker.streams[normalizedRequestID]
		if !stillPresent || current != existing {
			broker.mu.Unlock()
			continue
		}
		existing.mu.Lock()
		resetActiveStreamForRetryLocked(existing, conversationID, turnSeq, modelID, modelName, normalizedMode, latestUserText)
		existing.mu.Unlock()
		broker.mu.Unlock()
		return existing, nil
	}
}

func newActiveStream(requestID string, conversationID string, turnSeq int64, modelID string, modelName string, mode agentv1.AgentMode, latestUserText string) *ActiveStream {
	now := time.Now().UTC()
	return &ActiveStream{
		RequestID:                   requestID,
		ConversationID:              strings.TrimSpace(conversationID),
		TurnSeq:                     turnSeq,
		ModelID:                     strings.TrimSpace(modelID),
		ModelName:                   strings.TrimSpace(modelName),
		Mode:                        mode,
		LatestUserText:              strings.TrimSpace(latestUserText),
		Status:                      StreamStatusCreated,
		Backlog:                     make([]StreamEvent, 0, 64),
		Subscribers:                 make(map[string]*StreamSubscriber),
		PendingExecs:                make(map[string]runtimecore.PendingExec),
		PendingInteractions:         make(map[string]runtimecore.PendingInteraction),
		PartialToolCallIDs:          make(map[string]struct{}),
		PatchEditQueues:             make(map[string][]queuedPatchEditOperation),
		MCPToolServers:              make(map[string]string),
		RecentCompletedExecs:        make(map[uint32]time.Time),
		BackgroundShells:            make(map[string]*BackgroundShellState),
		BackgroundShellsByMessageID: make(map[uint32]string),
		BackgroundShellsByExecID:    make(map[string]string),
		BackgroundShellActions:      make(map[string]time.Time),
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
}

func updateActiveStreamContextLocked(stream *ActiveStream, conversationID string, turnSeq int64, modelID string, modelName string, mode agentv1.AgentMode, latestUserText string) {
	stream.ConversationID = strings.TrimSpace(conversationID)
	stream.TurnSeq = turnSeq
	stream.ModelID = strings.TrimSpace(modelID)
	stream.ModelName = strings.TrimSpace(modelName)
	stream.Mode = mode
	stream.LatestUserText = strings.TrimSpace(latestUserText)
	if stream.Status == "" {
		stream.Status = StreamStatusCreated
	}
	if stream.PendingExecs == nil {
		stream.PendingExecs = make(map[string]runtimecore.PendingExec)
	}
	if stream.PendingInteractions == nil {
		stream.PendingInteractions = make(map[string]runtimecore.PendingInteraction)
	}
	if stream.PartialToolCallIDs == nil {
		stream.PartialToolCallIDs = make(map[string]struct{})
	}
	if stream.PatchEditQueues == nil {
		stream.PatchEditQueues = make(map[string][]queuedPatchEditOperation)
	}
	if stream.BackgroundShells == nil {
		stream.BackgroundShells = make(map[string]*BackgroundShellState)
	}
	if stream.BackgroundShellsByMessageID == nil {
		stream.BackgroundShellsByMessageID = make(map[uint32]string)
	}
	if stream.BackgroundShellsByExecID == nil {
		stream.BackgroundShellsByExecID = make(map[string]string)
	}
	if stream.BackgroundShellActions == nil {
		stream.BackgroundShellActions = make(map[string]time.Time)
	}
	stream.UpdatedAt = time.Now().UTC()
}

func resetActiveStreamForRetryLocked(stream *ActiveStream, conversationID string, turnSeq int64, modelID string, modelName string, mode agentv1.AgentMode, latestUserText string) {
	if stream.TerminalCleanupTimer != nil {
		stream.TerminalCleanupTimer.Stop()
		stream.TerminalCleanupTimer = nil
	}
	stream.TerminalCleanupSeq.Add(1)
	stream.ConversationID = strings.TrimSpace(conversationID)
	stream.TurnSeq = turnSeq
	stream.ModelID = strings.TrimSpace(modelID)
	stream.ModelName = strings.TrimSpace(modelName)
	stream.Mode = mode
	stream.LatestUserText = strings.TrimSpace(latestUserText)
	stream.Status = StreamStatusCreated
	stream.ThinkingEffort = ""
	stream.SubagentModelOverrides = nil
	stream.CurrentModelCallID = ""
	stream.ProviderActive = false
	stream.ProviderCancel = nil
	stream.ProviderPassCount = 0
	stream.ActorMailbox = nil
	stream.ActorDone = nil
	stream.Phase = TurnPhaseIdle
	stream.PendingProviderAction = providerActionNone
	stream.PendingProviderCompletion = nil
	stream.CurrentProviderToken = 0
	stream.CurrentCompactionToken = 0
	stream.TimerTokens = make(map[string]uint64)
	stream.ProviderAccumulatedText = ""
	stream.ProviderAccumulatedReasoning = ""
	stream.ProviderAccumulatedReasoningSignature = ""
	stream.ProviderAccumulatedReasoningSignatureSource = ""
	stream.ProviderAccumulatedReasoningItemID = ""
	stream.ProviderAccumulatedReasoningStatus = ""
	stream.ProviderAccumulatedReasoningSummary = nil
	stream.ProviderSyntheticThinkingStartedAt = time.Time{}
	stream.ProviderSyntheticThinkingPublished = false
	stream.ProviderFinishReason = ""
	stream.ProviderUsage = turnUsageSnapshot{}
	stream.ProviderTerminalToolInvocation = false
	stream.PendingCompaction = nil
	stream.Backlog = make([]StreamEvent, 0, 64)
	stream.CheckpointConversation = nil
	stream.PendingExecs = make(map[string]runtimecore.PendingExec)
	stream.PendingInteractions = make(map[string]runtimecore.PendingInteraction)
	stream.PartialToolCallIDs = make(map[string]struct{})
	stream.PatchEditQueues = make(map[string][]queuedPatchEditOperation)
	stream.MCPToolServers = make(map[string]string)
	stream.WorkspacePaths = nil
	stream.TerminalsFolder = ""
	stream.RequestFileContents = nil
	stream.RecentCompletedExecs = make(map[uint32]time.Time)
	stream.BackgroundShells = make(map[string]*BackgroundShellState)
	stream.BackgroundShellsByMessageID = make(map[uint32]string)
	stream.BackgroundShellsByExecID = make(map[string]string)
	stream.BackgroundShellActions = make(map[string]time.Time)
	stream.CreatedAt = time.Now().UTC()
	stream.UpdatedAt = stream.CreatedAt
}

// Get 返回指定 request 对应的活动流句柄。
func (broker *StreamBroker) Get(requestID string) (*ActiveStream, bool) {
	if broker == nil {
		return nil, false
	}
	broker.mu.RLock()
	defer broker.mu.RUnlock()
	stream, ok := broker.streams[strings.TrimSpace(requestID)]
	return stream, ok
}

// Subscribe 为指定 request 注册一个新订阅者，并返回用于唤醒 backlog 消费的信号通道。
func (broker *StreamBroker) Subscribe(requestID string) (string, <-chan struct{}, error) {
	normalizedRequestID := strings.TrimSpace(requestID)
	if normalizedRequestID == "" {
		return "", nil, fmt.Errorf("request_id is required")
	}
	stream, ok := broker.Get(normalizedRequestID)
	if !ok || stream == nil {
		// RunSSE 可能先于 BidiAppend 到达。此时先创建一个占位活动流，
		// 等待后续上行把真实 conversation/model/mode 信息补齐。
		var err error
		stream, err = broker.OpenStream(normalizedRequestID, "", 0, "", "", agentv1.AgentMode_AGENT_MODE_AGENT, "")
		if err != nil {
			return "", nil, err
		}
	}
	subscriberID := fmt.Sprintf("sub-%d", broker.nextID.Add(1))
	subscriber := &StreamSubscriber{Signal: make(chan struct{}, subscriberSignalBufferSize)}

	stream.mu.Lock()
	broker.stopTerminalCleanupTimerLocked(stream)
	stream.Subscribers[subscriberID] = subscriber
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()

	return subscriberID, subscriber.Signal, nil
}

func (broker *StreamBroker) stopTerminalCleanupTimerLocked(stream *ActiveStream) {
	if stream == nil {
		return
	}
	stream.TerminalCleanupSeq.Add(1)
	if stream.TerminalCleanupTimer != nil {
		stream.TerminalCleanupTimer.Stop()
		stream.TerminalCleanupTimer = nil
	}
}

// Unsubscribe 移除并关闭指定订阅者，并返回移除后的剩余订阅者数量。
func (broker *StreamBroker) Unsubscribe(requestID string, subscriberID string) int {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return 0
	}
	remaining := 0
	stream.mu.Lock()
	if _, ok := stream.Subscribers[strings.TrimSpace(subscriberID)]; ok {
		delete(stream.Subscribers, strings.TrimSpace(subscriberID))
	}
	remaining = len(stream.Subscribers)
	stream.mu.Unlock()
	return remaining
}

func (broker *StreamBroker) OtherConversationRequestIDs(conversationID string, keepRequestID string) []string {
	normalizedConversationID := strings.TrimSpace(conversationID)
	normalizedKeepRequestID := strings.TrimSpace(keepRequestID)
	if normalizedConversationID == "" {
		return nil
	}
	type requestStream struct {
		requestID string
		stream    *ActiveStream
	}
	candidates := make([]requestStream, 0, 2)
	broker.mu.RLock()
	for requestID, stream := range broker.streams {
		if stream == nil || strings.TrimSpace(requestID) == normalizedKeepRequestID {
			continue
		}
		candidates = append(candidates, requestStream{
			requestID: requestID,
			stream:    stream,
		})
	}
	broker.mu.RUnlock()
	requestIDs := make([]string, 0, 2)
	for _, candidate := range candidates {
		stream := candidate.stream
		stream.mu.Lock()
		sameConversation := strings.TrimSpace(stream.ConversationID) == normalizedConversationID
		status := stream.Status
		phase := stream.Phase
		stream.mu.Unlock()
		terminalPhase := phase == TurnPhaseCanceled || phase == TurnPhaseCompleted || phase == TurnPhaseFailed
		if !sameConversation || isTerminalStreamStatus(status) || terminalPhase {
			continue
		}
		requestIDs = append(requestIDs, candidate.requestID)
	}
	return requestIDs
}

func (broker *StreamBroker) scheduleTerminalCleanup(requestID string) bool {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.Subscribers) > 0 {
		broker.stopTerminalCleanupTimerLocked(stream)
		return false
	}
	if stream.Status != StreamStatusCompleted && stream.Status != StreamStatusCanceled && stream.Status != StreamStatusFailed {
		broker.stopTerminalCleanupTimerLocked(stream)
		return false
	}
	sequence := stream.TerminalCleanupSeq.Add(1)
	if stream.TerminalCleanupTimer != nil {
		stream.TerminalCleanupTimer.Stop()
	}
	stream.TerminalCleanupTimer = time.AfterFunc(terminalStreamRetentionPeriod, func() {
		broker.runScheduledTerminalCleanup(requestID, sequence)
	})
	stream.UpdatedAt = time.Now().UTC()
	return true
}

func (broker *StreamBroker) runScheduledTerminalCleanup(requestID string, sequence uint64) {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return
	}
	stream.mu.Lock()
	if stream.TerminalCleanupSeq.Load() != sequence {
		stream.mu.Unlock()
		return
	}
	stream.TerminalCleanupTimer = nil
	if len(stream.Subscribers) > 0 {
		stream.mu.Unlock()
		return
	}
	if stream.Status != StreamStatusCompleted && stream.Status != StreamStatusCanceled && stream.Status != StreamStatusFailed {
		stream.mu.Unlock()
		return
	}
	stream.mu.Unlock()
	broker.RemoveIfIdle(requestID)
}

// RemoveIfIdle 在没有订阅者时移除终态流，或移除仍为空壳的占位流。
func (broker *StreamBroker) RemoveIfIdle(requestID string) bool {
	normalizedRequestID := strings.TrimSpace(requestID)
	if normalizedRequestID == "" {
		return false
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	stream, ok := broker.streams[normalizedRequestID]
	if !ok || stream == nil {
		return false
	}
	stream.mu.Lock()
	subscriberCount := len(stream.Subscribers)
	isActive := stream.ProviderActive
	hasBacklog := len(stream.Backlog) > 0
	hasConversation := strings.TrimSpace(stream.ConversationID) != ""
	status := stream.Status
	if status == StreamStatusCompleted || status == StreamStatusCanceled || status == StreamStatusFailed {
		broker.stopTerminalCleanupTimerLocked(stream)
	}
	stream.mu.Unlock()
	if subscriberCount > 0 {
		return false
	}
	if status == StreamStatusCompleted || status == StreamStatusCanceled || status == StreamStatusFailed {
		delete(broker.streams, normalizedRequestID)
		return true
	}
	if isActive || hasBacklog || hasConversation {
		return false
	}
	delete(broker.streams, normalizedRequestID)
	return true
}

// Publish 把一个事件写入 backlog，并唤醒当前所有订阅者读取 backlog。
func (broker *StreamBroker) Publish(requestID string, event StreamEvent) error {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", strings.TrimSpace(requestID))
	}
	stream.mu.Lock()
	if !event.End && isTerminalStreamStatus(stream.Status) {
		stream.mu.Unlock()
		return nil
	}
	stream.Backlog = append(stream.Backlog, event)
	stream.UpdatedAt = time.Now().UTC()
	subscribers := make([]*StreamSubscriber, 0, len(stream.Subscribers))
	for _, subscriber := range stream.Subscribers {
		subscribers = append(subscribers, subscriber)
	}
	stream.mu.Unlock()

	for _, subscriber := range subscribers {
		if subscriber == nil {
			continue
		}
		select {
		case subscriber.Signal <- struct{}{}:
		default:
		}
	}
	return nil
}

// ReadFromCursor 返回从 cursor 开始尚未消费的 backlog 事件副本。
func (broker *StreamBroker) ReadFromCursor(requestID string, cursor int) ([]StreamEvent, error) {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return nil, fmt.Errorf("request is not active: %s", strings.TrimSpace(requestID))
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(stream.Backlog) {
		return nil, nil
	}
	return append([]StreamEvent(nil), stream.Backlog[cursor:]...), nil
}

// Complete 把活动流标记为成功完成，并发布一个成功 endstream 事件。
func (broker *StreamBroker) Complete(requestID string, terminalCode string, terminalMessage string) error {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", strings.TrimSpace(requestID))
	}
	stream.mu.Lock()
	if stream.Status == StreamStatusCanceled || stream.Status == StreamStatusFailed || stream.Status == StreamStatusCompleted {
		stream.mu.Unlock()
		return nil
	}
	broker.stopTerminalCleanupTimerLocked(stream)
	stream.Status = StreamStatusCompleted
	subscriberCount := len(stream.Subscribers)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if err := broker.Publish(requestID, StreamEvent{
		End:                  true,
		TerminalErrorCode:    strings.TrimSpace(terminalCode),
		TerminalErrorMessage: strings.TrimSpace(terminalMessage),
	}); err != nil {
		return err
	}
	if subscriberCount == 0 {
		broker.scheduleTerminalCleanup(requestID)
	}
	return nil
}

// Fail 把活动流标记为失败，并发布一个失败 endstream 事件。
func (broker *StreamBroker) Fail(requestID string, terminalCode string, terminalMessage string) error {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", strings.TrimSpace(requestID))
	}
	stream.mu.Lock()
	broker.stopTerminalCleanupTimerLocked(stream)
	stream.Status = StreamStatusFailed
	subscriberCount := len(stream.Subscribers)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if err := broker.Publish(requestID, StreamEvent{
		End:                  true,
		TerminalErrorCode:    strings.TrimSpace(terminalCode),
		TerminalErrorMessage: strings.TrimSpace(terminalMessage),
	}); err != nil {
		return err
	}
	if subscriberCount == 0 {
		broker.scheduleTerminalCleanup(requestID)
	}
	return nil
}

// Cancel 主动取消活动流，并发布 canceled endstream。
func (broker *StreamBroker) Cancel(requestID string, terminalMessage string) error {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", strings.TrimSpace(requestID))
	}
	stream.mu.Lock()
	broker.stopTerminalCleanupTimerLocked(stream)
	if stream.ProviderCancel != nil {
		stream.ProviderCancel()
		stream.ProviderCancel = nil
	}
	stream.ProviderActive = false
	stream.Status = StreamStatusCanceled
	subscriberCount := len(stream.Subscribers)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if err := broker.Publish(requestID, StreamEvent{
		End:                  true,
		TerminalErrorCode:    "canceled",
		TerminalErrorMessage: firstNonEmpty(strings.TrimSpace(terminalMessage), "[canceled] User aborted request"),
	}); err != nil {
		return err
	}
	if subscriberCount == 0 {
		broker.scheduleTerminalCleanup(requestID)
	}
	return nil
}

// firstNonEmpty 返回第一个非空白字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
