package forwarder

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	"cursor/gen/agentv1"
)

type runRewindDecision struct {
	Evaluated               bool
	Apply                   bool
	Reason                  string
	SkipReason              string
	IncomingMessageID       string
	AnchorMessageID         string
	PrependUserMessageCount int
	HasClientTurnCount      bool
	ClientTurnCount         int
	ServerTailTurnSeq       int64
	ServerNextTurnSeq       int64
	TargetTurnSeq           int64
	TargetEntrySeq          int64
	TargetRequestID         string
	MatchCount              int
	MatchStrategy           string
	DroppedEntryCount       int
	DroppedTurnCount        int
	DroppedSeqStart         int64
	DroppedSeqEnd           int64
	PrefixEntries           []HistoryEntry
	ProjectedEntries        []HistoryEntry
	ProjectedEntryCount     int
	ProjectedTailTurnSeq    int64
	ProjectedLastMessageID  string
	ProjectedLastRequestID  string
}

type runRewindMatch struct {
	Entry HistoryEntry
}

type storedUserMessageEntry struct {
	Match   runRewindMatch
	Message *agentv1.UserMessage
}

func (service *Service) decideRunRewind(intent InboundIntent, conversation *ConversationFile) runRewindDecision {
	decision := runRewindDecision{ClientTurnCount: -1}
	if !shouldEvaluateRunRewind(intent) {
		return decision
	}
	decision.Evaluated = true
	decision.IncomingMessageID = strings.TrimSpace(intent.UserMessage.GetMessageId())
	decision.HasClientTurnCount = intent.ConversationState != nil
	if decision.HasClientTurnCount {
		decision.ClientTurnCount = len(intent.ConversationState.GetTurns())
	}
	if conversation != nil {
		decision.ServerTailTurnSeq = maxHistoryTurnSeq(conversation.Entries)
		decision.ServerNextTurnSeq = conversation.NextTurnSeq
	}
	if len(intent.PrependUserMessages) > 0 {
		return decideForkPrefixRewind(intent, conversation, decision)
	}
	if decision.IncomingMessageID == "" {
		decision.SkipReason = "missing_message_id"
		return decision
	}
	if conversation == nil || len(conversation.Entries) == 0 {
		decision.SkipReason = "message_id_not_found"
		return decision
	}

	matches := findUserMessageEntriesByMessageID(conversation.Entries, decision.IncomingMessageID)
	decision.MatchCount = len(matches)
	if len(matches) == 0 {
		decision.SkipReason = "message_id_not_found"
		return decision
	}
	selected, selectReason := selectRunRewindMatch(matches, decision.ClientTurnCount, decision.HasClientTurnCount)
	decision.TargetTurnSeq = selected.Entry.TurnSeq
	decision.TargetEntrySeq = selected.Entry.Seq
	decision.TargetRequestID = strings.TrimSpace(selected.Entry.RequestID)
	decision.MatchStrategy = "message_id"
	if decision.TargetTurnSeq <= 0 {
		decision.SkipReason = "target_turn_seq_missing"
		return decision
	}

	serverTailBeyondTarget := decision.ServerTailTurnSeq > decision.TargetTurnSeq
	clientBehindServerTail := decision.HasClientTurnCount && int64(decision.ClientTurnCount) < decision.ServerTailTurnSeq
	if !serverTailBeyondTarget && !clientBehindServerTail {
		decision.SkipReason = "message_id_at_active_tail"
		return decision
	}

	decision.Apply = true
	decision.Reason = selectReason
	decision.PrefixEntries = cloneHistoryEntries(prefixEntriesBeforeTurn(conversation.Entries, decision.TargetTurnSeq))
	decision.DroppedEntryCount, decision.DroppedTurnCount, decision.DroppedSeqStart, decision.DroppedSeqEnd = droppedEntryStats(conversation.Entries, decision.TargetTurnSeq)
	return decision
}

func decideForkPrefixRewind(intent InboundIntent, conversation *ConversationFile, decision runRewindDecision) runRewindDecision {
	decision.PrependUserMessageCount = len(intent.PrependUserMessages)
	if conversation == nil || len(conversation.Entries) == 0 {
		decision.SkipReason = "fork_prefix_history_missing"
		return decision
	}
	match, messageID, strategy, matchCount, found := selectForkPrefixRewindMatch(
		conversation.Entries,
		intent.PrependUserMessages,
		decision.ClientTurnCount,
		decision.HasClientTurnCount,
	)
	if !found {
		prefixEntries := buildClientForkPrefixEntries(intent.PrependUserMessages, intent.RequestID)
		if len(prefixEntries) == 0 {
			decision.SkipReason = "fork_prefix_message_not_found"
			return decision
		}
		decision.Apply = true
		decision.Reason = "fork_client_prefix_rebuild"
		decision.MatchStrategy = "client_prefix_rebuild"
		decision.PrefixEntries = prefixEntries
		decision.PrependUserMessageCount = len(prefixEntries)
		decision.TargetTurnSeq = maxHistoryTurnSeq(prefixEntries) + 1
		decision.AnchorMessageID = lastUserMessageID(prefixEntries)
		decision.IncomingMessageID = decision.AnchorMessageID
		decision.DroppedEntryCount, decision.DroppedTurnCount, decision.DroppedSeqStart, decision.DroppedSeqEnd = droppedEntryStatsAfterEntry(conversation.Entries, 0)
		return decision
	}
	decision.IncomingMessageID = messageID
	decision.AnchorMessageID = messageID
	decision.MatchStrategy = strategy
	decision.MatchCount = matchCount
	decision.TargetTurnSeq = match.Entry.TurnSeq + 1
	decision.TargetEntrySeq = match.Entry.Seq
	decision.TargetRequestID = strings.TrimSpace(match.Entry.RequestID)
	if decision.TargetTurnSeq <= 1 || decision.TargetEntrySeq <= 0 {
		decision.SkipReason = "fork_anchor_position_missing"
		return decision
	}

	decision.Apply = true
	decision.Reason = "prepend_user_message_anchor"
	decision.PrefixEntries = cloneHistoryEntries(prefixEntriesThroughEntry(conversation.Entries, match.Entry.Seq))
	decision.DroppedEntryCount, decision.DroppedTurnCount, decision.DroppedSeqStart, decision.DroppedSeqEnd = droppedEntryStatsAfterEntry(conversation.Entries, match.Entry.Seq)
	return decision
}

func buildClientForkPrefixEntries(messages []*agentv1.UserMessage, requestID string) []HistoryEntry {
	entries := make([]HistoryEntry, 0, len(messages))
	for _, message := range messages {
		if message == nil || (normalizedForkUserMessageText(message) == "" && strings.TrimSpace(message.GetMessageId()) == "") {
			continue
		}
		payload, err := protojson.Marshal(normalizeUserMessageForStorage(message))
		if err != nil {
			continue
		}
		entries = append(entries, HistoryEntry{
			Seq:       int64(len(entries) + 1),
			TurnSeq:   int64(len(entries) + 1),
			RequestID: strings.TrimSpace(requestID),
			Role:      "user",
			Kind:      "user_message",
			Payload:   payload,
		})
	}
	return entries
}

func selectForkPrefixRewindMatch(entries []HistoryEntry, messages []*agentv1.UserMessage, clientTurnCount int, hasClientTurnCount bool) (runRewindMatch, string, string, int, bool) {
	stored := collectStoredUserMessages(entries)
	if len(stored) == 0 {
		return runRewindMatch{}, "", "", 0, false
	}

	for messageIndex := len(messages) - 1; messageIndex >= 0; messageIndex-- {
		messageID := strings.TrimSpace(messages[messageIndex].GetMessageId())
		if messageID == "" {
			continue
		}
		matches := findStoredUserMessagesByID(stored, messageID)
		if len(matches) > 0 {
			selected := matches[len(matches)-1]
			return selected.Match, messageID, "message_id", len(matches), true
		}
	}

	for messageIndex := len(messages) - 1; messageIndex >= 0; messageIndex-- {
		messageText := normalizedForkUserMessageText(messages[messageIndex])
		if messageText == "" {
			continue
		}
		matches := findStoredUserMessagesByText(stored, messageText)
		if len(matches) > 0 {
			selected := matches[len(matches)-1]
			return selected.Match, strings.TrimSpace(selected.Message.GetMessageId()), "message_text", len(matches), true
		}
	}

	if match, count, ok := matchForkUserMessageSequence(stored, messages); ok {
		return match.Match, strings.TrimSpace(match.Message.GetMessageId()), "message_sequence", count, true
	}

	if inferred, ok := inferForkAnchorByTurnCount(stored, entries, clientTurnCount, hasClientTurnCount); ok {
		return inferred.Match, strings.TrimSpace(inferred.Message.GetMessageId()), "turn_count", 1, true
	}
	return runRewindMatch{}, "", "", 0, false
}

func collectStoredUserMessages(entries []HistoryEntry) []storedUserMessageEntry {
	stored := make([]storedUserMessageEntry, 0)
	for _, entry := range entries {
		if strings.TrimSpace(entry.Kind) != "user_message" || len(entry.Payload) == 0 {
			continue
		}
		message := &agentv1.UserMessage{}
		if err := protojson.Unmarshal(entry.Payload, message); err != nil {
			continue
		}
		stored = append(stored, storedUserMessageEntry{Match: runRewindMatch{Entry: entry}, Message: message})
	}
	return stored
}

func findStoredUserMessagesByID(stored []storedUserMessageEntry, messageID string) []storedUserMessageEntry {
	messageID = strings.TrimSpace(messageID)
	matches := make([]storedUserMessageEntry, 0, 1)
	for _, item := range stored {
		if strings.TrimSpace(item.Message.GetMessageId()) == messageID {
			matches = append(matches, item)
		}
	}
	return matches
}

func findStoredUserMessagesByText(stored []storedUserMessageEntry, text string) []storedUserMessageEntry {
	matches := make([]storedUserMessageEntry, 0, 1)
	for _, item := range stored {
		if normalizedForkUserMessageText(item.Message) == text {
			matches = append(matches, item)
		}
	}
	return matches
}

func matchForkUserMessageSequence(stored []storedUserMessageEntry, messages []*agentv1.UserMessage) (storedUserMessageEntry, int, bool) {
	clientTexts := make([]string, 0, len(messages))
	for _, message := range messages {
		if text := normalizedForkUserMessageText(message); text != "" {
			clientTexts = append(clientTexts, text)
		}
	}
	if len(clientTexts) < 2 {
		return storedUserMessageEntry{}, 0, false
	}
	for sequenceLength := len(clientTexts); sequenceLength >= 2; sequenceLength-- {
		clientSuffix := clientTexts[len(clientTexts)-sequenceLength:]
		matchCount := 0
		var selected storedUserMessageEntry
		for end := sequenceLength - 1; end < len(stored); end++ {
			matched := true
			for offset := 0; offset < sequenceLength; offset++ {
				if normalizedForkUserMessageText(stored[end-sequenceLength+1+offset].Message) != clientSuffix[offset] {
					matched = false
					break
				}
			}
			if matched {
				matchCount++
				selected = stored[end]
			}
		}
		if matchCount > 0 {
			return selected, matchCount, true
		}
	}
	return storedUserMessageEntry{}, 0, false
}

func inferForkAnchorByTurnCount(stored []storedUserMessageEntry, entries []HistoryEntry, clientTurnCount int, hasClientTurnCount bool) (storedUserMessageEntry, bool) {
	if !hasClientTurnCount || clientTurnCount <= 0 {
		return storedUserMessageEntry{}, false
	}
	serverTail := maxHistoryTurnSeq(entries)
	delta := serverTail - int64(clientTurnCount)
	if delta < -1 || delta > 1 {
		return storedUserMessageEntry{}, false
	}
	var selected *storedUserMessageEntry
	for index := range stored {
		item := stored[index]
		if item.Match.Entry.TurnSeq > int64(clientTurnCount) {
			continue
		}
		if selected == nil || item.Match.Entry.TurnSeq >= selected.Match.Entry.TurnSeq {
			candidate := item
			selected = &candidate
		}
	}
	if selected == nil {
		return storedUserMessageEntry{}, false
	}
	return *selected, true
}

func forkUserMessageText(message *agentv1.UserMessage) string {
	if message == nil {
		return ""
	}
	if text := strings.TrimSpace(message.GetText()); text != "" {
		return text
	}
	return strings.TrimSpace(message.GetRichText())
}

func normalizedForkUserMessageText(message *agentv1.UserMessage) string {
	return strings.Join(strings.Fields(forkUserMessageText(message)), " ")
}

func prefixEntriesThroughEntry(entries []HistoryEntry, anchorSeq int64) []HistoryEntry {
	prefix := make([]HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Seq <= anchorSeq {
			prefix = append(prefix, entry)
		}
	}
	return prefix
}

func droppedEntryStatsAfterEntry(entries []HistoryEntry, anchorSeq int64) (int, int, int64, int64) {
	droppedTurns := make(map[int64]struct{})
	var droppedEntries int
	var seqStart int64
	var seqEnd int64
	for _, entry := range entries {
		if entry.Seq <= anchorSeq {
			continue
		}
		droppedEntries++
		if entry.TurnSeq > 0 {
			droppedTurns[entry.TurnSeq] = struct{}{}
		}
		if seqStart == 0 || entry.Seq < seqStart {
			seqStart = entry.Seq
		}
		if entry.Seq > seqEnd {
			seqEnd = entry.Seq
		}
	}
	return droppedEntries, len(droppedTurns), seqStart, seqEnd
}

func shouldEvaluateRunRewind(intent InboundIntent) bool {
	if strings.TrimSpace(intent.Kind) != "run" || intent.Prewarm || intent.UserMessage == nil {
		return false
	}
	return conversationActionCase(intent.ClientMessage) == "user_message_action"
}

func findUserMessageEntriesByMessageID(entries []HistoryEntry, messageID string) []runRewindMatch {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" || len(entries) == 0 {
		return nil
	}
	matches := make([]runRewindMatch, 0, 1)
	for _, item := range collectStoredUserMessages(entries) {
		if strings.TrimSpace(item.Message.GetMessageId()) == messageID {
			matches = append(matches, item.Match)
		}
	}
	return matches
}

func selectRunRewindMatch(matches []runRewindMatch, clientTurnCount int, hasClientTurnCount bool) (runRewindMatch, string) {
	if len(matches) == 0 {
		return runRewindMatch{}, "no_match"
	}
	if hasClientTurnCount && clientTurnCount >= 0 {
		targetTurnSeq := int64(clientTurnCount) + 1
		for _, match := range matches {
			if match.Entry.TurnSeq == targetTurnSeq {
				return match, "client_turn_count_aligned"
			}
		}
		var candidate *runRewindMatch
		for index := range matches {
			match := matches[index]
			if match.Entry.TurnSeq <= int64(clientTurnCount) {
				continue
			}
			if candidate == nil || earlierHistoryEntry(match.Entry, candidate.Entry) {
				candidate = &match
			}
		}
		if candidate != nil {
			return *candidate, "first_match_after_client_turn_count"
		}
	}
	selected := matches[0]
	for _, match := range matches[1:] {
		if earlierHistoryEntry(match.Entry, selected.Entry) {
			selected = match
		}
	}
	return selected, "earliest_message_id_match"
}

func earlierHistoryEntry(left HistoryEntry, right HistoryEntry) bool {
	if left.TurnSeq != right.TurnSeq {
		return left.TurnSeq < right.TurnSeq
	}
	if left.Seq != right.Seq {
		return left.Seq < right.Seq
	}
	return left.CreatedAt.Before(right.CreatedAt)
}

func maxHistoryTurnSeq(entries []HistoryEntry) int64 {
	var maxTurnSeq int64
	for _, entry := range entries {
		if entry.TurnSeq > maxTurnSeq {
			maxTurnSeq = entry.TurnSeq
		}
	}
	return maxTurnSeq
}

func prefixEntriesBeforeTurn(entries []HistoryEntry, targetTurnSeq int64) []HistoryEntry {
	prefix := make([]HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.TurnSeq < targetTurnSeq {
			prefix = append(prefix, entry)
		}
	}
	return prefix
}

func droppedEntryStats(entries []HistoryEntry, targetTurnSeq int64) (int, int, int64, int64) {
	droppedTurns := make(map[int64]struct{})
	var droppedEntries int
	var seqStart int64
	var seqEnd int64
	for _, entry := range entries {
		if entry.TurnSeq < targetTurnSeq {
			continue
		}
		droppedEntries++
		if entry.TurnSeq > 0 {
			droppedTurns[entry.TurnSeq] = struct{}{}
		}
		if entry.Seq > 0 && (seqStart == 0 || entry.Seq < seqStart) {
			seqStart = entry.Seq
		}
		if entry.Seq > seqEnd {
			seqEnd = entry.Seq
		}
	}
	return droppedEntries, len(droppedTurns), seqStart, seqEnd
}

func buildRunRewindProjection(prefix []HistoryEntry, entries []HistoryEntry) []HistoryEntry {
	projection := make([]HistoryEntry, 0, len(prefix)+len(entries))
	projection = append(projection, cloneHistoryEntries(prefix)...)
	projection = append(projection, cloneHistoryEntries(entries)...)
	for index := range projection {
		projection[index].Seq = int64(index + 1)
	}
	return projection
}

func cloneHistoryEntries(entries []HistoryEntry) []HistoryEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]HistoryEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = entry
		cloned[index].Payload = append([]byte(nil), entry.Payload...)
	}
	return cloned
}

func finalizeRunRewindProjection(decision *runRewindDecision, entries []HistoryEntry) {
	if decision == nil || !decision.Apply {
		return
	}
	decision.ProjectedEntries = buildRunRewindProjection(decision.PrefixEntries, entries)
	decision.ProjectedEntryCount = len(decision.ProjectedEntries)
	decision.ProjectedTailTurnSeq = maxHistoryTurnSeq(decision.ProjectedEntries)
	decision.ProjectedLastMessageID = lastUserMessageID(decision.ProjectedEntries)
	decision.ProjectedLastRequestID = lastHistoryRequestID(decision.ProjectedEntries)
}

func lastUserMessageID(entries []HistoryEntry) string {
	stored := collectStoredUserMessages(entries)
	if len(stored) == 0 {
		return ""
	}
	return strings.TrimSpace(stored[len(stored)-1].Message.GetMessageId())
}

func lastHistoryRequestID(entries []HistoryEntry) string {
	for index := len(entries) - 1; index >= 0; index-- {
		if requestID := strings.TrimSpace(entries[index].RequestID); requestID != "" {
			return requestID
		}
	}
	return ""
}

func (service *Service) applyRunRewindToConversation(conversation *ConversationFile, decision runRewindDecision, intent InboundIntent, turnSeq int64) {
	if conversation == nil || !decision.Apply {
		return
	}
	conversation.Entries = nil
	conversation.NextEntrySeq = 1
	conversation.NextTurnSeq = 1
	appendEntriesInPlace(conversation, decision.ProjectedEntries)
	applyRunRewindConversationState(conversation, intent, turnSeq)
	deriveConversationLoopState(conversation)
}

func applyRunRewindConversationState(conversation *ConversationFile, intent InboundIntent, turnSeq int64) {
	if conversation == nil {
		return
	}
	conversation.TokenDetailsUsedTokens = 0
	if conversation.TokenDetailsMaxTokens == 0 {
		conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	}
	clearConversationAutoCompactionState(conversation)
	conversation.LatestRequestPrefix = nil
	conversation.LastProviderCall = nil
	conversation.CurrentLoopID = fmt.Sprintf("%d:%s", turnSeq, strings.TrimSpace(intent.RequestID))
	conversation.CurrentLoopStatus = "running"
	conversation.CurrentRequestID = strings.TrimSpace(intent.RequestID)
	conversation.CurrentTurnSeq = turnSeq
}

func applyRunRewindMetadata(conversation *ConversationFile, source *ConversationFile, intent InboundIntent, turnSeq int64) {
	if conversation == nil {
		return
	}
	if source != nil {
		if strings.TrimSpace(source.ConversationID) != "" {
			conversation.ConversationID = strings.TrimSpace(source.ConversationID)
		}
		if strings.TrimSpace(source.RootConversationID) != "" {
			conversation.RootConversationID = strings.TrimSpace(source.RootConversationID)
		}
		conversation.ParentConversationID = strings.TrimSpace(source.ParentConversationID)
		conversation.ParentToolCallID = strings.TrimSpace(source.ParentToolCallID)
		conversation.SubagentTypeName = strings.TrimSpace(source.SubagentTypeName)
		if strings.TrimSpace(source.Mode) != "" {
			conversation.Mode = strings.TrimSpace(source.Mode)
		}
		if source.TokenDetailsMaxTokens > 0 {
			conversation.TokenDetailsMaxTokens = source.TokenDetailsMaxTokens
		}
	}
	applyRunRewindConversationState(conversation, intent, turnSeq)
}

func (service *Service) logRunRewindDecision(requestID string, conversationID string, eventName string, decision runRewindDecision) {
	if service == nil || !decision.Evaluated {
		return
	}
	fields := map[string]any{
		"message_id":                 decision.IncomingMessageID,
		"anchor_message_id":          decision.AnchorMessageID,
		"prepend_user_message_count": decision.PrependUserMessageCount,
		"apply":                      decision.Apply,
		"reason":                     decision.Reason,
		"skip_reason":                decision.SkipReason,
		"fork_match_strategy":        decision.MatchStrategy,
		"target_turn_seq":            decision.TargetTurnSeq,
		"target_entry_seq":           decision.TargetEntrySeq,
		"target_request_id":          decision.TargetRequestID,
		"server_tail_turn_seq":       decision.ServerTailTurnSeq,
		"server_next_turn_seq":       decision.ServerNextTurnSeq,
		"match_count":                decision.MatchCount,
		"dropped_entry_count":        decision.DroppedEntryCount,
		"dropped_turn_count":         decision.DroppedTurnCount,
		"dropped_seq_start":          decision.DroppedSeqStart,
		"dropped_seq_end":            decision.DroppedSeqEnd,
		"projected_entry_count":      decision.ProjectedEntryCount,
		"projected_tail_turn_seq":    decision.ProjectedTailTurnSeq,
		"projected_last_message_id":  decision.ProjectedLastMessageID,
		"projected_last_request_id":  decision.ProjectedLastRequestID,
	}
	if decision.HasClientTurnCount {
		fields["client_turn_count"] = decision.ClientTurnCount
	} else {
		fields["client_turn_count"] = nil
	}
	service.debug.LogRuntime(context.Background(), requestID, conversationID, eventName, fields)
}
