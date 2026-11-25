import type { PlanNode } from '../../api/types'
import Badge from '../../components/Badge'
import { formatNumber } from '../../format'

interface PlanRowProps {
  node: PlanNode
  analyze: boolean
}

export default function PlanRow({ node, analyze }: PlanRowProps) {
  // makin dalam node-nya, makin menjorok ke kanan
  const indent = { paddingLeft: `${node.depth * 1.25 + 1}rem` }
  return (
    <tr>
      <td style={indent}>
        <span className="flex flex-wrap items-center gap-2 whitespace-nowrap">
          {node.depth > 0 && <span className="text-muted">└</span>}
          <strong>{node.node_type}</strong>
          {node.join_type && <Badge tone="outline">{node.join_type}</Badge>}
          {node.relation && <code>{node.relation}</code>}
          {node.index_name && <Badge tone="lime">{node.index_name}</Badge>}
        </span>
      </td>
      <td className="max-w-[36ch] break-words font-mono text-[0.8rem] text-muted">{node.condition}</td>
      <td className="whitespace-nowrap text-right tabular-nums">
        {formatNumber(node.startup_cost)}..{formatNumber(node.total_cost)}
      </td>
      <td className="text-right tabular-nums">{formatNumber(node.plan_rows, 0)}</td>
      {analyze && <td className="text-right tabular-nums">{formatNumber(node.actual_rows, 0)}</td>}
      {analyze && <td className="text-right tabular-nums">{formatNumber(node.actual_total_ms, 3)}</td>}
    </tr>
  )
}
