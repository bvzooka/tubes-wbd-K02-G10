import type { ExplainResult } from '../../api/types'
import Panel from '../../components/Panel'
import StatCard from '../../components/StatCard'
import { formatNumber } from '../../format'
import PlanRow from './PlanRow'

interface ExplainOutputProps {
  result: ExplainResult
}

export default function ExplainOutput({ result }: ExplainOutputProps) {
  const plan = result.plan
  if (!plan) {
    // MySQL dan MariaDB cuma punya output teks
    return (
      <Panel icon="layers" title="Hasil EXPLAIN">
        <pre className="m-0 whitespace-pre-wrap p-6 font-mono text-[0.85rem]">{result.raw_text}</pre>
      </Panel>
    )
  }

  return (
    <>
      <div className="mb-5 grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-4">
        <StatCard icon="hash" label="Total cost" value={formatNumber(plan.total_cost)} />
        <StatCard icon="layers" label="Jumlah node" value={plan.nodes.length} />
        {plan.planning_time_ms !== null && (
          <StatCard icon="history" label="Planning (ms)" value={formatNumber(plan.planning_time_ms, 3)} />
        )}
        {plan.execution_time_ms !== null && (
          <StatCard icon="bolt" label="Eksekusi (ms)" value={formatNumber(plan.execution_time_ms, 3)} accent />
        )}
      </div>

      <Panel icon="layers" title="Pohon plan" count={plan.nodes.length}>
        <table className="table">
          <thead>
            <tr>
              <th>Node</th>
              <th>Kondisi</th>
              <th className="text-right">Cost</th>
              <th className="text-right">Baris (estimasi)</th>
              {result.analyze && <th className="text-right">Baris (aktual)</th>}
              {result.analyze && <th className="text-right">Waktu (ms)</th>}
            </tr>
          </thead>
          <tbody>
            {plan.nodes.map((node, i) => (
              <PlanRow key={i} node={node} analyze={result.analyze} />
            ))}
          </tbody>
        </table>
      </Panel>

      <details className="card">
        <summary className="cursor-pointer font-semibold">JSON mentah</summary>
        <pre className="scroll-thin mt-3 max-h-[420px] overflow-auto font-mono text-[0.8rem]">{plan.json}</pre>
      </details>
    </>
  )
}
