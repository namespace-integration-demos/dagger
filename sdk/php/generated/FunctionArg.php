<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace Dagger;

/**
 * An argument accepted by a function.
 *
 * This is a specification for an argument at function definition time, not an argument passed at function call time.
 */
class FunctionArg extends Client\AbstractObject implements Client\IdAble
{
    public function defaultValue(): Json
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('defaultValue');
        return new \Dagger\Json((string)$this->queryLeaf($leafQueryBuilder, 'defaultValue'));
    }

    public function description(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('description');
        return (string)$this->queryLeaf($leafQueryBuilder, 'description');
    }

    /**
     * A unique identifier for this FunctionArg.
     */
    public function id(): FunctionArgId
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('id');
        return new \Dagger\FunctionArgId((string)$this->queryLeaf($leafQueryBuilder, 'id'));
    }

    public function name(): string
    {
        $leafQueryBuilder = new \Dagger\Client\QueryBuilder('name');
        return (string)$this->queryLeaf($leafQueryBuilder, 'name');
    }

    public function typeDef(): TypeDef
    {
        $innerQueryBuilder = new \Dagger\Client\QueryBuilder('typeDef');
        return new \Dagger\TypeDef($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));
    }
}