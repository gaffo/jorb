package org.gaffo.jorb.acjtest;

import org.gaffo.jorb.ACJ;
import org.gaffo.jorb.ACJTest;

public class Job1 extends ACJ
{

    private ACJTest test;

    public Job1(ACJTest test)
    {
        this.test = test;
    }

    public void process(I1 i1)
    {
        test.verifyCallback(i1);
    }

}
