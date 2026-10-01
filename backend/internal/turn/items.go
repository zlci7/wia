package turn

import (
	"fmt"
	"strings"
)

func applyItemTransfers(snapshot Snapshot, output *Output, effects []itemTransferEffect, sources map[string]mechanicSource) error {
	items, transfers := output.Items, output.ItemTransfers
	entities := map[string]bool{"player": true}
	for _, c := range snapshot.Characters {
		entities[c.EntityID] = true
	}
	order := nextEffectOrder(*output) - 1
	for index, effect := range effects {
		field := fmt.Sprintf("item_transfers[%d]", index)
		effect.InstanceID, effect.ActionID = strings.TrimSpace(effect.InstanceID), strings.TrimSpace(effect.ActionID)
		effect.FromHolderID, effect.FromLocationID = strings.TrimSpace(effect.FromHolderID), strings.TrimSpace(effect.FromLocationID)
		effect.ToHolderID, effect.ToLocationID = strings.TrimSpace(effect.ToHolderID), strings.TrimSpace(effect.ToLocationID)
		source, ok := validMechanicSource(sources, effect.ActionID)
		current, exists := items[effect.InstanceID]
		if !ok || !exists || current.HolderID != effect.FromHolderID || current.LocationID != effect.FromLocationID || (effect.ToHolderID == "") == (effect.ToLocationID == "") || (effect.ToHolderID != "" && !entities[effect.ToHolderID]) || (effect.ToLocationID != "" && !placeExists(snapshot.Definition, effect.ToLocationID)) || (current.HolderID == effect.ToHolderID && current.LocationID == effect.ToLocationID) {
			return coordinationInvalid("item_transfer_invalid", field, "unique-authorized-transfer-from-current-placement")
		}
		next := current
		next.HolderID, next.LocationID = effect.ToHolderID, effect.ToLocationID
		next.SourceEvent, next.UpdatedTurn, next.Version = source.resultID, snapshot.Summary.TurnSeq+1, current.Version+1
		order++
		items[effect.InstanceID] = next
		transfers = append(transfers, ItemTransfer{Before: current, After: next, ActionID: effect.ActionID, SourceEventID: source.resultID, Order: order})
	}
	output.Items, output.ItemTransfers = items, transfers
	return nil
}

type itemTransferEffect struct {
	InstanceID     string `json:"instance_id"`
	FromHolderID   string `json:"from_holder_id,omitempty"`
	FromLocationID string `json:"from_location_id,omitempty"`
	ToHolderID     string `json:"to_holder_id,omitempty"`
	ToLocationID   string `json:"to_location_id,omitempty"`
	ActionID       string `json:"action_id"`
}

func itemCoordinationContract() string {
	return "\nitem_transfers 每项含 instance_id、from_holder_id、from_location_id、to_holder_id、to_location_id、action_id；归属为一个持有人或一个地点，空字段省略。根据实际行动与 NPC 本人决定判断拾取、交付或夺取等结果，来源为当前 succeeded/partial 行动。按发生顺序填写，后项从前项归属继续；人物移动前后的物品操作按情节顺序理解。可转移给其他已定义 NPC，行动者不必是原持有人；不替未作决定的 NPC 补同意。"
}
