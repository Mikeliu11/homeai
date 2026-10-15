import { Link } from "react-router-dom";

export default function Help() {
  return (
    <div className="stack" style={{ maxWidth: 720 }}>
      <h1 style={{ margin: 0 }}>How this works</h1>
      <div className="card stack">
        <p><strong>1. Paste what you have about a helper.</strong> On <Link to="/home">Home</Link>, drop in a text thread, a screenshot of one, an email, or notes from memory. The assistant turns it into a <em>terms card</em>: the rates, when each applies, and from what date. Nothing is agreed until you press <strong>Confirm these terms</strong>. Edit amounts or dates first if it misread something, or reject the card and tell it what was wrong.</p>
        <p><strong>2. Paste the month's bill.</strong> The assistant lays it out as a <em>bill card</em>, one line per visit. Press <strong>Confirm this bill</strong> and the pricing engine checks every line against the terms in force on that day. The engine is plain code, not the model: it never guesses.</p>
        <p><strong>3. Read the verdict and approve.</strong> On <Link to="/bills">Bills</Link>, each bill shows three numbers: what the bill says, what its lines add to, and what the agreement supports. Lines that were never agreed, that admit two readings, or that cannot be priced need your call in the approve dialog. Once approved, a bill is locked and a message to the helper is drafted for you.</p>
        <p><strong>Put on hold</strong> marks a bill as waiting (for example while you ask the helper about a line) and keeps your note; it can still be approved later. <strong>Re-check against terms</strong> re-runs the engine with the terms on record now, useful after you confirm a rate change. Neither pays anything.</p>
        <p><strong>What the assistant remembers.</strong> Household context from your conversations (who is who, pay methods, schedule changes) is kept in memory and shown under <Link to="/memories">Memories</Link>. It is context, never an agreement: rates and bills only count once you confirm them, and if a memory and a record disagree, the record wins.</p>
        <p><strong>The assistant never confirms or approves anything itself.</strong> It can only draft cards. Every change to agreements or money goes through a button you press.</p>
      </div>
    </div>
  );
}
