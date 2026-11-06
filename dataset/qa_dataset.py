"""
QA Dataset Sampling Tool

```
pip install pandas pyarrow
pip install openai
```

# 采样数据
python dataset/qa_dataset.py sample \
  --queries ~/dataset/queries.parquet \
  --corpus ~/dataset/corpus.parquet \
  --qrels ~/dataset/qrels.parquet \
  --nq 100 \
  --output_dir ./dataset/samples

# 生成答案(基于采样结果)
# 可选：追加 --model <model-name> 指定 OpenAI 兼容服务中的模型
# 切换模型时使用新的 --output_dir；断点续跑仍会复用已有答案
python dataset/qa_dataset.py generate \
  --input_dir ./dataset/samples \
  --output_dir ./dataset/samples

# 展示结果
python dataset/qa_dataset.py show \
  --input_dir ./dataset/samples \
  -n 1
"""

import os
from pathlib import Path
import argparse
import numbers
import uuid

import pandas as pd
import openai


DEFAULT_QA_MODEL = "gpt-5.6-sol"


def read_parquet(path):
    return pd.read_parquet(path)


def save_to_parquet(df: pd.DataFrame, path: str):
    """Save DataFrame to parquet file"""
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    # A stopped process must not leave a half-written resume checkpoint.
    temporary_path = Path(path).with_name(f".{Path(path).name}.{uuid.uuid4().hex}.tmp")
    descriptor = os.open(temporary_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o666)
    os.close(descriptor)
    try:
        if Path(path).exists():
            os.chmod(temporary_path, Path(path).stat().st_mode & 0o777)
        df.to_parquet(temporary_path, index=False)
        os.replace(temporary_path, path)
    finally:
        temporary_path.unlink(missing_ok=True)
    print(f"Saved to {path}")


def print_stats(df: pd.DataFrame, name: str):
    """Print statistics of a DataFrame"""
    print(f"\n{name} Statistics:")
    print(f"- Total records: {len(df)}")
    if "id" in df.columns:
        print(f"- Unique ids: {df['id'].nunique()}")
    if "qid" in df.columns:
        print(f"- Unique qids: {df['qid'].nunique()}")
    if "pid" in df.columns:
        print(f"- Unique pids: {df['pid'].nunique()}")


def sample_data(
    queries: pd.DataFrame, corpus: pd.DataFrame, qrels: pd.DataFrame, nq=1000
):
    """
    Sample data from the dataset with validation checks.

    Args:
        queries: DataFrame with qid and text columns (one-to-one)
        corpus: DataFrame with pid and text columns (one-to-one)
        qrels: DataFrame with qid and pid columns (many-to-many)
        nq: Number of queries to sample (default: 1000)

    Returns:
        Tuple of (sampled_queries, sampled_corpus, sampled_qrels)
    """
    if nq <= 0:
        raise ValueError("nq must be positive")
    # 1. Filter qrels to only include qids that exist in queries
    valid_qids = set(queries["id"])
    qrels = qrels[qrels["qid"].isin(valid_qids)]

    # 2. Filter qrels to only include pids that exist in corpus
    valid_pids = set(corpus["id"])
    qrels = qrels[qrels["pid"].isin(valid_pids)]

    # 3. Sample queries (ensure we have enough qrels samples for each)
    # Get qids with most associated pids to ensure diversity
    qid_counts = qrels["qid"].value_counts()
    sampled_qids = qid_counts.nlargest(min(nq, len(qid_counts))).index

    # 4. Get all pids associated with sampled qids
    sampled_qrels = qrels[qrels["qid"].isin(sampled_qids)]
    sampled_pids = set(sampled_qrels["pid"])

    # 5. Add extra pids from corpus for redundancy (20% of sampled pids)
    extra_pids = set(corpus["id"].sample(int(0.2 * len(sampled_pids))))
    all_pids = sampled_pids.union(extra_pids)

    # 6. Create final sampled datasets
    sampled_queries = queries[queries["id"].isin(sampled_qids)]
    sampled_corpus = corpus[corpus["id"].isin(all_pids)]

    return sampled_queries, sampled_corpus, sampled_qrels


class QAAnsweringSystem:
    def __init__(
        self, queries: pd.DataFrame, corpus: pd.DataFrame, qrels: pd.DataFrame
    ):
        """
        Initialize QA system with data

        Args:
            queries: DataFrame with qid and text columns
            corpus: DataFrame with pid and text columns
            qrels: DataFrame with qid and pid mapping
        """
        self.queries = queries
        self.corpus = corpus
        self.qrels = qrels
        # Sampling, showing results and a completed resume are fully offline.
        # Credentials are only required when an answer actually needs generating.
        self.client = None

        # Create lookup dictionaries
        self.qid_to_text = dict(zip(queries["id"], queries["text"]))
        self.pid_to_text = dict(zip(corpus["id"], corpus["text"]))
        self.qid_to_pids = qrels.groupby("qid")["pid"].apply(list).to_dict()

    def get_context_for_qid(self, qid: str) -> str:
        """
        Get all relevant text for a query ID

        Args:
            qid: Query ID to search for

        Returns:
            Combined context text from all related passages
        """
        if qid not in self.qid_to_pids:
            raise ValueError("Question ID not found")

        context_parts = []
        print(f"Context for Question ID {qid}: {self.qid_to_pids[qid]}")
        for pid in self.qid_to_pids[qid]:
            if pid in self.pid_to_text:
                context_parts.append(self.pid_to_text[pid])

        return "\n\n".join(context_parts)

    def answer_question(self, qid: str, model: str = DEFAULT_QA_MODEL) -> str:
        """
        Use OpenAI API to answer question based on qid context

        Args:
            qid: Query ID to answer
            model: OpenAI model to use

        Returns:
            Generated answer from LLM
        """
        if qid not in self.qid_to_text:
            raise ValueError("Question ID not found")

        question = self.qid_to_text[qid]
        context = self.get_context_for_qid(qid)

        if not context:
            raise ValueError("No context found for this question")

        prompt = f"""Answer the question based on the context below. Keep the answer concise.

Question: {question}

Context: {context}

Answer:"""
        if self.client is None:
            self.client = openai.Client(
                api_key=os.getenv("OPENAI_API_KEY"),
                base_url=os.getenv("OPENAI_BASE_URL"),
            )
        response = self.client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": prompt}],
            temperature=0.3,
        )
        if not response.choices or response.choices[0].message is None:
            raise ValueError("LLM returned empty or filtered response")
        answer = response.choices[0].message.content
        if not isinstance(answer, str) or not answer.strip():
            raise ValueError("LLM returned empty or filtered response")
        return answer


def sample_command(args):
    """Handle sample command"""
    # Load data
    print("Loading data...")
    queries = read_parquet(args.queries)
    corpus = read_parquet(args.corpus)
    qrels = read_parquet(args.qrels)

    # Print original stats
    print("\nOriginal Dataset Statistics:")
    print_stats(queries, "Queries")
    print_stats(corpus, "Corpus")
    print_stats(qrels, "Qrels")

    # Sample data
    print(f"\nSampling {args.nq} queries...")
    sampled_queries, sampled_corpus, sampled_qrels = sample_data(
        queries, corpus, qrels, args.nq
    )

    # Print sampled stats
    print("\nSampled Dataset Statistics:")
    print_stats(sampled_queries, "Sampled Queries")
    print_stats(sampled_corpus, "Sampled Corpus")
    print_stats(sampled_qrels, "Sampled Qrels")

    # Save sampled data
    print("\nSaving sampled data...")
    save_to_parquet(sampled_queries, f"{args.output_dir}/queries.parquet")
    save_to_parquet(sampled_corpus, f"{args.output_dir}/corpus.parquet")
    save_to_parquet(sampled_qrels, f"{args.output_dir}/qrels.parquet")
    print("\nSampling completed successfully!")


def generate_answers(
    input_dir: str,
    output_dir: str,
    max_retries: int = 3,
    *,
    model: str = DEFAULT_QA_MODEL,
):
    """
    Generate answers for sampled queries with resume support

    Saved answers are reused even when the model changes. Use a new output
    directory to regenerate all answers with a different model.

    Args:
        input_dir: Directory containing sampled queries/corpus/qrels
        output_dir: Directory to save answer files
        max_retries: Maximum retry attempts for failed queries
        model: OpenAI-compatible model to use for answer generation
    """
    print("\nLoading sampled data...")
    queries = read_parquet(f"{input_dir}/queries.parquet")
    corpus = read_parquet(f"{input_dir}/corpus.parquet")
    qrels = read_parquet(f"{input_dir}/qrels.parquet")

    # Try to load existing answers if any
    answers_path = f"{output_dir}/answers.parquet"
    qa_pairs_path = f"{output_dir}/qas.parquet"

    def load_checkpoint(path, columns):
        try:
            frame = read_parquet(path)
        except FileNotFoundError:
            return pd.DataFrame(columns=columns)
        if not set(columns).issubset(frame.columns):
            raise ValueError(f"Invalid checkpoint columns in {path}: expected {columns}")
        return frame[columns]

    existing_answers = load_checkpoint(answers_path, ["id", "text"])
    existing_qas = load_checkpoint(qa_pairs_path, ["qid", "aid"])
    if existing_answers["id"].isna().any() or existing_answers["id"].duplicated().any():
        raise ValueError("Answer checkpoint IDs must be non-null and unique")
    # If a write was interrupted between the two files, retry dangling pairs
    # and retain any orphan answers so a previously assigned ID is never reused.
    valid_answers = existing_answers["text"].map(
        lambda text: isinstance(text, str) and bool(text.strip())
    )
    existing_qas = existing_qas[existing_qas["aid"].isin(existing_answers.loc[valid_answers, "id"])]
    processed_qids = set(existing_qas["qid"])
    print(f"\nFound {len(processed_qids)} previously processed queries")

    qa_system = QAAnsweringSystem(queries, corpus, qrels)

    answers = existing_answers.to_dict("records")
    qa_pairs = existing_qas.to_dict("records")
    answer_ids = existing_answers["id"].tolist()
    string_ids = bool(answer_ids) and all(isinstance(aid, str) for aid in answer_ids)
    if not string_ids and not all(isinstance(aid, numbers.Integral) for aid in answer_ids):
        raise ValueError("Answer IDs must consistently be integers or strings")
    answer_id_counter = max(answer_ids, default=0) + 1 if not string_ids else None

    for qid in queries["id"]:
        if qid in processed_qids:
            continue

        retry_count = 0
        while retry_count <= max_retries:
            try:
                answer_text = qa_system.answer_question(qid, model=model)
                aid = str(uuid.uuid4()) if string_ids else answer_id_counter
                answers.append({"id": aid, "text": answer_text})
                qa_pairs.append({"qid": qid, "aid": aid})
                if not string_ids:
                    answer_id_counter += 1
                processed_qids.add(qid)

                # Save progress after each successful answer
                save_to_parquet(pd.DataFrame(answers), answers_path)
                save_to_parquet(pd.DataFrame(qa_pairs), qa_pairs_path)
                print(f"Processed qid: {qid}")
                break
            except (openai.APIError, openai.APIConnectionError) as e:
                retry_count += 1
                if retry_count > max_retries:
                    print(
                        f"\nFailed to process qid {qid} after {max_retries} attempts: {str(e)}"
                    )
                    # Save failed state
                    save_to_parquet(pd.DataFrame(answers, columns=["id", "text"]), answers_path)
                    save_to_parquet(pd.DataFrame(qa_pairs, columns=["qid", "aid"]), qa_pairs_path)
                else:
                    print(f"\nRetry {retry_count} for qid {qid}...")

    print("\nAnswer generation completed!")
    print(f"Total queries: {len(queries)}")
    print(f"Successfully processed: {len(qa_pairs)}")
    print(f"Failed queries: {len(queries) - len(qa_pairs)}")


def show_results(input_dir: str, n: int = 5):
    """
    Show n random results with question, context and answer

    Args:
        input_dir: Directory containing the QA data
        n: Number of results to show (default: 5)
    """
    if n < 0:
        raise ValueError("n must not be negative")
    print(f"\nShowing up to {n} random results:")

    # Load data
    queries = read_parquet(f"{input_dir}/queries.parquet")
    corpus = read_parquet(f"{input_dir}/corpus.parquet")
    qrels = read_parquet(f"{input_dir}/qrels.parquet")
    qa_pairs = read_parquet(f"{input_dir}/qas.parquet")
    answers = read_parquet(f"{input_dir}/answers.parquet")

    # Create QA system for context lookup
    qa_system = QAAnsweringSystem(queries, corpus, qrels)

    # Get first n QA pairs
    for _, row in qa_pairs.sample(min(n, len(qa_pairs))).iterrows():
        qid = row["qid"]
        aid = row["aid"]

        # Get question
        question = qa_system.qid_to_text[qid]

        # Get context
        context = qa_system.get_context_for_qid(qid)

        # Get answer
        answer = answers[answers["id"] == aid]["text"].values[0]

        print("\n" + "=" * 50)
        print(f"Question (qid={qid}):\n{question}")
        print("\nContext:")
        print(context)
        print(f"\nAnswer (aid={aid}):\n{answer}")
        print("=" * 50 + "\n")


def _model_argument(value: str) -> str:
    if not value.strip():
        raise argparse.ArgumentTypeError("model must not be empty or whitespace-only")
    return value


def main():
    # Set up command line arguments
    parser = argparse.ArgumentParser(description="QA Dataset Tool")
    subparsers = parser.add_subparsers(dest="command", required=True)

    # Sample command
    sample_parser = subparsers.add_parser("sample", help="Sample dataset")
    sample_parser.add_argument(
        "--queries", type=str, required=True, help="Path to queries parquet file"
    )
    sample_parser.add_argument(
        "--corpus", type=str, required=True, help="Path to corpus parquet file"
    )
    sample_parser.add_argument(
        "--qrels", type=str, required=True, help="Path to qrels parquet file"
    )
    sample_parser.add_argument(
        "--nq", type=int, default=1000, help="Number of queries to sample"
    )
    sample_parser.add_argument(
        "--output_dir", type=str, default="./save", help="Output directory"
    )
    sample_parser.set_defaults(func=sample_command)

    # Generate command
    generate_parser = subparsers.add_parser("generate", help="Generate answers")
    generate_parser.add_argument(
        "--input_dir", type=str, required=True, help="Directory with sampled data"
    )
    generate_parser.add_argument(
        "--output_dir", type=str, default="./save", help="Output directory"
    )
    generate_parser.add_argument(
        "--model",
        type=_model_argument,
        default=DEFAULT_QA_MODEL,
        help=(
            f"Model for answer generation (default: {DEFAULT_QA_MODEL}). "
            "Resume reuses saved answers; use a new --output_dir when changing models."
        ),
    )
    generate_parser.set_defaults(
        func=lambda args: generate_answers(
            args.input_dir, args.output_dir, model=args.model
        )
    )

    # Show command
    show_parser = subparsers.add_parser("show", help="Show QA results")
    show_parser.add_argument(
        "--input_dir", type=str, required=True, help="Directory with QA data"
    )
    show_parser.add_argument(
        "-n", type=int, default=5, help="Number of results to show (default: 5)"
    )
    show_parser.set_defaults(func=lambda args: show_results(args.input_dir, args.n))

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
