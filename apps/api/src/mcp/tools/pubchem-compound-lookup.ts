import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().optional().describe("Compound query — name, numeric CID, or InChIKey. Auto-detected by format."),
  name: z.string().optional().describe("Compound common or scientific name (e.g. 'aspirin', 'fentanyl', 'tetrahydrocannabinol')."),
  cid: z.union([z.string(), z.number()]).optional().describe("PubChem Compound ID (CID), e.g. 2244 for aspirin."),
  inchi_key: z.string().optional().describe("Standard InChIKey (e.g. 'BSYNRYMUTXBXSQ-UHFFFAOYSA-N')."),
  max_synonyms: z.number().int().min(1).max(500).optional().describe("Max synonyms to return (default 50)."),
});

toolRegistry.register({
  name: "pubchem_compound_lookup",
  description:
    "**PubChem — chemistry/drug compound lookup, free no-auth, 100M+ compounds.** Single mode: lookup by name OR CID OR InChIKey → comprehensive record. Returns: CID, IUPAC name, primary common name, molecular formula + weight, canonical SMILES (structural notation), InChIKey (canonical hash identifier), XLogP (lipophilicity / drug-likeness signal — typically 0-5 for drugs, higher = more fat-soluble), CAS numbers parsed from synonyms, full synonym list (often hundreds — useful for ER on alternate brand names, IUPAC variants, foreign-language names, street names for controlled substances). Tested aspirin → CID 2244, formula C9H8O4, MW 180.16, IUPAC '2-acetyloxybenzoic acid', InChIKey BSYNRYMUTXBXSQ-UHFFFAOYSA-N, 697 synonyms including CAS 50-78-2. Pairs with `openfda_search` (drug labels reference compounds by name) and `clinicaltrials_search` (compound-X trials). Forensic toxicology + pharma ER + controlled substance identification.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "pubchem_compound_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "pubchem_compound_lookup failed");
    return res.output;
  },
});
